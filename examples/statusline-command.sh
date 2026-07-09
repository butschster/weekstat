#!/bin/bash
# Claude Code status line.
# Line 1: <project-relative dir>  <git branch>  [context bar] pct used/max
# Line 2: weekly pacing — how much of the 7-day quota is left and the per-day
#         budget (% per remaining day) that makes it last until the reset.
#
# Input: JSON on stdin (see Claude Code statusLine schema).

input=$(cat)

# ---- feed the weekstat daemon: persist this raw stdin snapshot atomically ----
# The Go daemon (~/.claude/tools/weekstat) watches this file and turns the
# rolling 7-day quota into weekly history + a dashboard. Cheap, best-effort.
_si="$HOME/.claude/.statusline-input.json"
printf '%s' "$input" > "$_si.tmp" 2>/dev/null && mv -f "$_si.tmp" "$_si" 2>/dev/null

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
  today_spent=$(jq -r '.today.spent_pct // empty' "$HOME/.claude/week-stats.json" 2>/dev/null)
  tcol='\033[2m'
  if [ -n "$today_spent" ] && [ -n "$budget_day" ]; then
    if   awk "BEGIN{exit !($today_spent > $budget_day)}"       2>/dev/null; then tcol='\033[31m'
    elif awk "BEGIN{exit !($today_spent > $budget_day*0.8)}"   2>/dev/null; then tcol='\033[33m'
    else                                                                        tcol='\033[32m'
    fi
  fi

  # ---- render line 2 ----
  printf '\n\033[2m wk:\033[0m %b%s\033[0m' "$pcol" "$qbar"
  printf ' \033[2mleft\033[0m %s%%' "$remain_pct"
  [ -n "$today_spent" ] && printf ' \033[2m·\033[0m \033[2mtoday\033[0m %b%.0f%%\033[0m' "$tcol" "$today_spent"
  if [ -n "$budget_day" ]; then
    printf ' \033[2m·\033[0m \033[2mbudget\033[0m \033[1m%s%%/day\033[0m \033[2mfor %sd\033[0m' "$budget_day" "$days_left"
    printf ' \033[2m·\033[0m %b%s\033[0m' "$pcol" "$pmark"
  fi
fi
