#!/bin/bash
# Claude Code status line.
# Line 1: <project-relative dir>  <git branch>  [context bar] pct used/max
# Line 2: weekly pacing — how much of the 7-day quota is left and the per-day
#         budget (% per remaining day) that makes it last until the reset.
#
# Input: JSON on stdin (see Claude Code statusLine schema).

input=$(cat)

# ---- tag the snapshot with this session's account ----
# Each Claude account has its own weekly window; the daemon keeps them apart by
# this key. It comes from the session's own config (CLAUDE_CONFIG_DIR aware):
# key = sha256(accountUuid)[:12], label = the account's email (else the
# organization name), plan = tier. Only non-secret fields of .claude.json are
# read; .credentials.json is not. An auto-generated organization name
# ("<email>'s Organization") is not used as a label.
# Fields are split on \x1f, not tabs: bash collapses empty tab-separated fields.
#
# A session logged in with CLAUDE_CODE_OAUTH_TOKEN (`claude setup-token`) is a
# different account from the one in .claude.json, so its key is a hash of the
# token instead (the token itself goes nowhere) and its label comes only from
# ~/.claude/weekstat-accounts.json — {"<key>": "name"}, which also overrides the
# label of any other account.
_sha12() {
  if command -v sha256sum >/dev/null; then sha256sum | cut -c1-12
  else shasum -a 256 | cut -c1-12; fi
}
_au="" _al="" _ap="" acct_key=""
if [ -n "$CLAUDE_CODE_OAUTH_TOKEN" ]; then
  acct_key=$(printf 'token:%s' "$CLAUDE_CODE_OAUTH_TOKEN" | _sha12)
  _ap=token
else
  _cj="${CLAUDE_CONFIG_DIR:-$HOME}/.claude.json"
  IFS=$'\x1f' read -r _au _al _ap <<<"$(
    jq -r '.oauthAccount // {} | [
      (.accountUuid // ""),
      (if (.emailAddress // "") != "" then .emailAddress
       else (.organizationName // "") | if test("@") or endswith("\u0027s Organization") then "" else . end end),
      (if (.userRateLimitTier // "") != "" then .userRateLimitTier else (.organizationType // "") end)
    ] | join("\u001f")' "$_cj" 2>/dev/null
  )"
  [ -n "$_au" ] && acct_key=$(printf '%s' "$_au" | _sha12)
fi
if [ -n "$acct_key" ]; then
  _nm=$(jq -r --arg k "$acct_key" '.[$k] // empty' "$HOME/.claude/weekstat-accounts.json" 2>/dev/null)
  [ -n "$_nm" ] && _al=$_nm
  _tagged=$(printf '%s' "$input" | jq -c --arg k "$acct_key" --arg l "$_al" --arg p "$_ap" \
    '. + {weekstat: {account: {key: $k, label: $l, plan: $p}}}' 2>/dev/null)
  [ -n "$_tagged" ] && input=$_tagged     # on a jq failure keep the untagged snapshot
fi

# ---- feed the weekstat daemon: persist this raw stdin snapshot atomically ----
# The Go daemon (~/.claude/tools/weekstat) watches this file and turns the
# rolling 7-day quota into weekly history + a dashboard. Cheap, best-effort.
# A unique temp file per render: sessions render concurrently, and a shared
# .tmp would let one session's write clobber another's half-written file.
_si="$HOME/.claude/.statusline-input.json"
if [ -n "$input" ] && _st=$(mktemp "$_si.XXXXXX" 2>/dev/null); then
  { printf '%s' "$input" > "$_st" && mv -f "$_st" "$_si"; } 2>/dev/null || rm -f "$_st"
fi

# ---- pull everything out in one jq pass (tab-separated; paths may contain spaces) ----
IFS=$'\t' read -r cwd project_dir model used_pct used_tok max_tok <<<"$(
  printf '%s' "$input" | jq -r '[
    (.cwd // .workspace.current_dir // ""),
    (.workspace.project_dir // .cwd // ""),
    (.model.display_name // "?"),
    (.context_window.used_percentage // 0),
    (.context_window.total_input_tokens // 0),
    (.context_window.context_window_size // 0)
  ] | @tsv'
)"

# ---- directory, relative to project root ----
root_name=$(basename "$project_dir")
if [ "$cwd" = "$project_dir" ] || [ -z "$project_dir" ]; then
  dir_display="${root_name:-$(basename "$cwd")}"
else
  rel="${cwd#"$project_dir"/}"
  dir_display="$root_name/$rel"
fi

# ---- git branch (cheap; silently empty outside a repo) ----
branch=$(git -C "$cwd" branch --show-current 2>/dev/null)

# ---- helpers ----
fmt_k() {                     # 84523 -> 85k ; 200000 -> 200k ; 940 -> 940
  local n=${1%%.*}
  if [ "${n:-0}" -ge 1000 ] 2>/dev/null; then
    echo "$(((n + 500) / 1000))k"
  else
    echo "${n:-0}"
  fi
}

used_int=${used_pct%%.*}; used_int=${used_int:-0}

# context color: green <70, yellow <90, red otherwise
if   [ "$used_int" -lt 70 ]; then ucol='\033[32m'
elif [ "$used_int" -lt 90 ]; then ucol='\033[33m'
else                              ucol='\033[31m'
fi

# ---- context progress bar ----
bar_width=14
filled=$((used_int * bar_width / 100))
[ "$filled" -gt "$bar_width" ] && filled=$bar_width
[ "$filled" -lt 0 ] && filled=0
empty=$((bar_width - filled))
bar=""
for ((i = 0; i < filled; i++)); do bar+="█"; done
for ((i = 0; i < empty;  i++)); do bar+="░"; done

# ---- render line 1 ----
printf '\033[1;34m%s\033[0m' "$dir_display"                                # dir (bold blue)
[ -n "$branch" ] && printf '  \033[35m%s\033[0m' "$branch"                  # branch (magenta)
printf '  %b%s %d%%\033[0m' "$ucol" "$bar" "$used_int"                      # context bar + pct
printf ' \033[2m%s/%s\033[0m' "$(fmt_k "$used_tok")" "$(fmt_k "$max_tok")"  # used/max tokens

# ======================================================================
# Line 2 — weekly pacing, from the subscription's rolling 7-day quota
# (rate_limits.seven_day, provided by Claude Code on stdin).
#
#   left      = 100 - used%                          (quota still available)
#   days_left = fractional days until the window resets
#   budget    = left / days_left  = % you may spend per remaining day
#
# The budget is recomputed on every render, so if you burn more than your
# share today, the remaining days automatically get a smaller per-day
# budget (and if you underspend, it grows). No cost math, no transcript
# scanning — purely the quota Claude Code already reports.
# ======================================================================

seven_pct=$(printf '%s' "$input" | jq -r '.rate_limits.seven_day.used_percentage // empty')
seven_reset=$(printf '%s' "$input" | jq -r '.rate_limits.seven_day.resets_at // empty')

# ---- Claude Code service health (status.claude.com, polled by the daemon) ----
# An outage is flagged with a red badge in front of the usage line (the
# figures stay — they are still the last known values); degraded performance
# gets an amber note at the end. The incident title lives in the tray/dashboard.
IFS=$'\t' read -r svc_outage svc_level svc_label <<<"$(
  jq -r '[(.service.outage // false), (.service.level // ""), (.service.label // "")] | @tsv' \
    "$HOME/.claude/week-stats.json" 2>/dev/null
)"

if [ -n "$seven_pct" ]; then
  now_epoch=$(date +%s)

  # remaining quota, clamped to [0,100]
  remain_pct=$(awk "BEGIN{r=100-($seven_pct); if(r<0)r=0; if(r>100)r=100; printf \"%.0f\", r}")

  # ---- small 10-cell bar of quota USED ----
  qbar_w=10
  qfilled=$(awk "BEGIN{f=int(($seven_pct)*$qbar_w/100+0.5); if(f>$qbar_w)f=$qbar_w; if(f<0)f=0; print f}")
  qbar=""
  for ((i = 0; i < qfilled;  i++)); do qbar+="█"; done
  for ((i = qfilled; i < qbar_w; i++)); do qbar+="░"; done

  if [ -n "$seven_reset" ]; then
    # fractional days until reset (floor 0.25 so we never divide by ~0)
    days_left=$(awk "BEGIN{d=($seven_reset-$now_epoch)/86400; if(d<0.25)d=0.25; printf \"%.1f\", d}")
    # per-day budget that spreads the remaining quota evenly over the days left
    budget_day=$(awk "BEGIN{printf \"%.1f\", ($remain_pct)/($days_left)}")
    # ideal even-burn: fraction of the 7-day window already elapsed
    ideal_used=$(awk "BEGIN{u=(1-($seven_reset-$now_epoch)/(7*86400))*100; if(u<0)u=0; if(u>100)u=100; printf \"%.1f\", u}")

    # pace colour/label: are we spending slower or faster than the even burn?
    if   awk "BEGIN{exit !($seven_pct <= $ideal_used)}" 2>/dev/null; then
      pcol='\033[32m'; pmark='on track ✓'
    elif awk "BEGIN{exit !($seven_pct <= $ideal_used + 10)}" 2>/dev/null; then
      pcol='\033[33m'; pmark='slightly over'
    else
      pcol='\033[31m'; pmark='over budget'
    fi
  else
    days_left=""; budget_day=""; pcol='\033[2m'; pmark=""
  fi

  # ---- today's consumption — collected from the weekstat daemon's output ----
  # (delta of used% since the day's first reading; the daemon owns the history).
  # Colour it against the daily budget: red if today already exceeds it.
  # The figures are this session's account's (another session may be the
  # daemon's active one); nothing until the daemon has seen this account, and
  # the top level for a daemon without "accounts" or a session without a key.
  # Prefer the daemon's STABLE budget/day (start-of-day remaining ÷ whole days
  # left) over the naive stdin estimate, so today's own spend doesn't move it.
  IFS=$'\x1f' read -r today_spent ws_budget acct_label <<<"$(
    jq -r --arg k "$acct_key" '(if $k != "" and .accounts != null
        then (.accounts[$k] // {}) | {t: .today_spent_pct, b: .budget_per_day_pct, l: .label}
        else {t: .today.spent_pct, b: .quota.budget_per_day_pct, l: null} end) as $v | [
      ($v.t // ""), ($v.b // ""), ($v.l // "")
    ] | map(tostring) | join("\u001f")' "$HOME/.claude/week-stats.json" 2>/dev/null
  )"
  [ -n "$ws_budget" ] && budget_day=$ws_budget
  tcol='\033[2m'
  if [ -n "$today_spent" ] && [ -n "$budget_day" ]; then
    if   awk "BEGIN{exit !($today_spent > $budget_day)}"       2>/dev/null; then tcol='\033[31m'
    elif awk "BEGIN{exit !($today_spent > $budget_day*0.8)}"   2>/dev/null; then tcol='\033[33m'
    else                                                                        tcol='\033[32m'
    fi
  fi

  # ---- render line 2 ----
  printf '\n'
  [ "$svc_outage" = "true" ] && printf '\033[1;31m ⛔ %s\033[0m \033[2m·\033[0m' "${svc_label:-outage}"
  # name the account this line is about: the local part of its email (or the
  # daemon's "plan · key" label until it has one)
  acct_show=${_al:-$acct_label}
  [ -n "$acct_show" ] && printf ' \033[36m%s\033[0m' "${acct_show%%@*}"
  printf '\033[2m wk:\033[0m %b%s\033[0m' "$pcol" "$qbar"
  printf ' \033[2mleft\033[0m %s%%' "$remain_pct"
  [ -n "$today_spent" ] && printf ' \033[2m·\033[0m \033[2mtoday\033[0m %b%.0f%%\033[0m' "$tcol" "$today_spent"
  if [ -n "$budget_day" ]; then
    printf ' \033[2m·\033[0m \033[2mbudget\033[0m \033[1m%s%%/day\033[0m \033[2mfor %sd\033[0m' "$budget_day" "$days_left"
    printf ' \033[2m·\033[0m %b%s\033[0m' "$pcol" "$pmark"
  fi
  # degraded performance / maintenance: API works, but flag it
  [ "$svc_level" = "degraded" ] && printf ' \033[2m·\033[0m \033[33m▲ %s\033[0m' "$svc_label"
fi

# Claude Code drops the statusline when the command fails, and the last
# command above is a "[ … ] && printf" that returns 1 when the test is false.
exit 0
