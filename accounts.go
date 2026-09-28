package main

// Per-account quota state.
//
// Every Claude account has its own 7-day window. The statusline script tags
// each snapshot with the account it belongs to ("weekstat.account": a hashed
// key, a label — the account's email, else the organization name — and the
// plan, read from the session's own .claude.json), and the daemon keeps a separate window + day history per key.
// A reading from another account therefore lands in that account's state
// instead of looking like a window reset of the current one.
//
// Snapshots without a key (an older statusline script) are routed by their
// window (resets_at) to an "anonymous" account — see State.route.

import (
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"time"
)

// legacyKey names the history kept from before accounts were tracked, and the
// first account seen without a key.
const legacyKey = "legacy"

// AccountInfo is the account block the statusline script adds to the snapshot.
type AccountInfo struct {
	Key   string `json:"key"`   // sha256(accountUuid)[:12]
	Label string `json:"label"` // email address, else organization name
	Plan  string `json:"plan"`  // userRateLimitTier / organizationType
}

// AccountRef identifies the account a set of figures belongs to.
type AccountRef struct {
	Key   string `json:"key"`
	Label string `json:"label"` // display label, see State.displayLabel
	Plan  string `json:"plan,omitempty"`
}

// AccountSummary is the short per-account line in week-stats.json and /accounts.
type AccountSummary struct {
	AccountRef
	Active          bool    `json:"active"`
	UpdatedAt       string  `json:"updated_at"`
	UsedPct         float64 `json:"used_pct"`
	RemainingPct    float64 `json:"remaining_pct"`
	BudgetPerDayPct float64 `json:"budget_per_day_pct"`
	TodaySpentPct   float64 `json:"today_spent_pct"`
	Pace            string  `json:"pace"`
}

func newState() *State {
	return &State{
		AccountState: AccountState{Days: map[string]*DayStat{}},
		Accounts:     map[string]*AccountState{},
	}
}

func isAnonymous(key string) bool {
	return key == legacyKey || strings.HasPrefix(key, "anon-")
}

// normalize fixes up a freshly loaded state: nil maps, and the migration of a
// pre-accounts file (top-level window + days only) into the legacy account.
func (st *State) normalize() {
	if st.Days == nil {
		st.Days = map[string]*DayStat{}
	}
	if st.Accounts == nil {
		st.Accounts = map[string]*AccountState{}
	}
	if len(st.Accounts) == 0 {
		if st.WindowEndUnix != 0 || len(st.Days) > 0 {
			legacy := st.AccountState
			st.Accounts[legacyKey] = &legacy
			st.Active = legacyKey
		}
		return
	}
	for _, acc := range st.Accounts {
		if acc.Days == nil {
			acc.Days = map[string]*DayStat{}
		}
		acc.Label = accountLabel(acc.Label)
	}
	if _, ok := st.Accounts[st.Active]; !ok {
		st.Active = st.latest(func(string) bool { return true })
	}
	st.AccountState = *st.Accounts[st.Active]
}

// latest is the most recently updated account key accepted by keep ("" if none).
func (st *State) latest(keep func(string) bool) string {
	best := ""
	for _, k := range st.sortedKeys() {
		if !keep(k) {
			continue
		}
		if best == "" || st.Accounts[k].UpdatedUnix > st.Accounts[best].UpdatedUnix {
			best = k
		}
	}
	return best
}

func (st *State) sortedKeys() []string {
	keys := make([]string, 0, len(st.Accounts))
	for k := range st.Accounts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// rollsOver reports whether a reading whose window ends at endUnix is the
// next weekly window of acc: acc's window has ended and the new one ends a
// week after it (± 1 h). An account without a window yet takes any reading.
func (acc *AccountState) rollsOver(endUnix int64, now time.Time) bool {
	if acc.WindowEndUnix == 0 {
		return true
	}
	return now.Unix() >= acc.WindowEndUnix-3600 &&
		sameWindow(acc.WindowEndUnix+int64(weekDur.Seconds()), endUnix)
}

// route picks (creating if needed) the account key a reading belongs to.
//
//   - With a key: that account. A new key first adopts an anonymous account
//     in the same window, or whose window it rolls over — the history recorded
//     before the statusline script started sending keys.
//   - Without a key: the account whose window matches resets_at; else the
//     account whose window it rolls over (keyed ones too — a keyless reading
//     from a session whose .claude.json could not be read); else a new
//     anonymous account. The active account is tried first each time. Two
//     keyless accounts with windows within an hour of each other can't be told
//     apart and merge.
//
// A keyed reading whose window is neither its account's window nor the week
// after it carries a stale tag: the session is still logged in (in memory) to
// an account it logged out of, while .claude.json already names the new
// login. It goes to the account in that window, else to a new one keyed by
// the tag and the window's hour of the week (stable from week to week), and
// trusted is false: its label and plan describe the other account.
func (st *State) route(key string, endUnix int64, now time.Time) (routed string, trusted bool) {
	st.normalize()
	rolls := func(k string) bool { return st.Accounts[k].rollsOver(endUnix, now) }
	if key != "" {
		if acc, ok := st.Accounts[key]; ok {
			nextWeek := acc.WindowEndUnix + int64(weekDur.Seconds()) // an early reset may start it ahead of time
			if sameWindow(acc.WindowEndUnix, endUnix) || sameWindow(nextWeek, endUnix) || acc.rollsOver(endUnix, now) {
				return key, true
			}
			if k := st.matchWindow(endUnix, func(string) bool { return true }); k != "" {
				return k, false
			}
			k := fmt.Sprintf("%s-w%03d", key, (endUnix%int64(weekDur.Seconds())+1800)/3600%168)
			if _, ok := st.Accounts[k]; !ok {
				st.Accounts[k] = &AccountState{Days: map[string]*DayStat{}}
			}
			return k, false
		}
		anon := st.matchWindow(endUnix, isAnonymous)
		if anon == "" {
			anon = st.pick(func(k string) bool { return isAnonymous(k) && rolls(k) })
		}
		if anon != "" {
			st.Accounts[key] = st.Accounts[anon]
			delete(st.Accounts, anon)
			if st.Active == anon {
				st.Active = key
			}
			return key, true
		}
		st.Accounts[key] = &AccountState{Days: map[string]*DayStat{}}
		return key, true
	}

	if k := st.matchWindow(endUnix, func(string) bool { return true }); k != "" {
		return k, false
	}
	if k := st.pick(rolls); k != "" {
		return k, false
	}

	k := legacyKey
	if _, taken := st.Accounts[k]; taken {
		base := "anon-" + time.Unix(endUnix, 0).Local().Format("0102")
		k = base
		for i := 2; st.Accounts[k] != nil; i++ {
			k = fmt.Sprintf("%s-%d", base, i)
		}
	}
	st.Accounts[k] = &AccountState{Days: map[string]*DayStat{}}
	return k, false
}

// pick is the active account if keep accepts it, else the most recently
// updated one keep accepts ("" if none).
func (st *State) pick(keep func(string) bool) string {
	if _, ok := st.Accounts[st.Active]; ok && keep(st.Active) {
		return st.Active
	}
	return st.latest(keep)
}

// matchWindow is the account accepted by keep whose window is endUnix, the
// active account first.
func (st *State) matchWindow(endUnix int64, keep func(string) bool) string {
	if acc, ok := st.Accounts[st.Active]; ok && keep(st.Active) && sameWindow(acc.WindowEndUnix, endUnix) {
		return st.Active
	}
	for _, k := range st.sortedKeys() {
		if keep(k) && sameWindow(st.Accounts[k].WindowEndUnix, endUnix) {
			return k
		}
	}
	return ""
}

// needsBackup reports whether the next state write is the first one an older
// (pre-accounts) build can't fully read back: the migration of a pre-accounts
// file, or the first write with a second account. An older build keeps only
// the active account and drops the rest.
func (st *State) needsBackup() bool {
	if !st.loadedFile || st.loadedAccounts >= 2 {
		return false
	}
	return st.loadedAccounts == 0 || len(st.Accounts) >= 2
}

// backupState copies the state file to <path>.bak, once: an existing .bak
// (the oldest, pre-accounts copy) is never overwritten.
func backupState(path string) {
	bak := path + ".bak"
	if _, err := os.Stat(bak); err == nil {
		return
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	if err := os.WriteFile(bak, b, 0o644); err != nil {
		log.Printf("backup state: %v", err)
		return
	}
	log.Printf("state backed up to %s (for a rollback to a pre-accounts build)", bak)
}

// pruneAccounts drops inactive accounts not seen for historyDays.
func (st *State) pruneAccounts(now time.Time) {
	cutoff := now.AddDate(0, 0, -historyDays).Unix()
	for k, acc := range st.Accounts {
		if k != st.Active && acc.UpdatedUnix < cutoff {
			delete(st.Accounts, k)
		}
	}
}

// account is the state for key, or the (possibly empty) active mirror.
func (st *State) account(key string) *AccountState {
	if acc, ok := st.Accounts[key]; ok {
		return acc
	}
	return &st.AccountState
}

// accountLabel drops the auto-generated organization name of a personal
// account ("<email>'s Organization"): it says nothing the email doesn't, and a
// label like that means the statusline had no email to send. A plain email is
// kept — it is the label the owner wants to see.
func accountLabel(name string) string {
	if strings.HasSuffix(name, "'s Organization") {
		return ""
	}
	return name
}

// planLabel shortens a raw tier such as "default_claude_max_20x" to "max 20x".
func planLabel(raw string) string {
	p := strings.ToLower(strings.TrimSpace(raw))
	p = strings.TrimPrefix(p, "default_")
	p = strings.TrimPrefix(p, "claude_")
	return strings.ReplaceAll(p, "_", " ")
}

// displayLabel is the account's email (else organization name), or "plan · key" when the label is
// missing, auto-generated (see accountLabel) or shared with another account (just
// the key without a plan).
func (st *State) displayLabel(key string) string {
	acc := st.Accounts[key]
	if label := accountLabel(acc.Label); label != "" {
		unique := true
		for k, other := range st.Accounts {
			if k != key && accountLabel(other.Label) == label {
				unique = false
			}
		}
		if unique {
			return acc.Label
		}
	}
	if p := planLabel(acc.Plan); p != "" {
		return p + " · " + key
	}
	return key
}

func (st *State) ref(key string) AccountRef {
	return AccountRef{Key: key, Label: st.displayLabel(key), Plan: planLabel(st.Accounts[key].Plan)}
}

func (st *State) summary(key string, now time.Time) AccountSummary {
	acc := st.Accounts[key]
	o := windowOutput(acc, now)
	return AccountSummary{
		AccountRef:      st.ref(key),
		Active:          key == st.Active,
		UpdatedAt:       time.Unix(acc.UpdatedUnix, 0).Local().Format("2006-01-02 15:04:05"),
		UsedPct:         o.Quota.UsedPct,
		RemainingPct:    o.Quota.RemainingPct,
		BudgetPerDayPct: o.Quota.BudgetPerDayPct,
		TodaySpentPct:   o.Today.SpentPct,
		Pace:            o.Quota.Pace,
	}
}

// accountList is every account, most recently updated first.
func (st *State) accountList(now time.Time) []AccountSummary {
	keys := st.sortedKeys()
	sort.SliceStable(keys, func(i, j int) bool {
		return st.Accounts[keys[i]].UpdatedUnix > st.Accounts[keys[j]].UpdatedUnix
	})
	out := make([]AccountSummary, 0, len(keys))
	for _, k := range keys {
		out = append(out, st.summary(k, now))
	}
	return out
}

// output is the figures of account key, tagged with the account and the
// summary of every account.
func (st *State) output(key string, now time.Time) Output {
	o := windowOutput(st.account(key), now)
	if _, ok := st.Accounts[key]; ok {
		ref := st.ref(key)
		o.Account = &ref
		o.Accounts = make(map[string]AccountSummary, len(st.Accounts))
		for k := range st.Accounts {
			o.Accounts[k] = st.summary(k, now)
		}
	}
	return o
}

// computeOutput is the active account's figures.
func computeOutput(st *State, now time.Time) Output {
	return st.output(st.Active, now)
}

// historyOutput is the active account's daily time-series.
func historyOutput(st *State, now time.Time, days int) History {
	return accountHistory(st.account(st.Active), now, days)
}
