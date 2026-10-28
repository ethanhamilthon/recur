#!/usr/bin/env bash
set -euo pipefail

REPO="${RECUR_AUTOPILOT_REPO:-ethanhamilthon/recur}"
AUTHOR="${RECUR_AUTOPILOT_AUTHOR:-ethanhamilthon}"
AUTOPILOT_HOME="${RECUR_AUTOPILOT_HOME:-$HOME/.recur-autopilot}"
SOURCE_HOME="${RECUR_SOURCE_HOME:-$HOME/.recur}"
POLL_INTERVAL="${RECUR_AUTOPILOT_INTERVAL:-15}"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ACTIVE_WORKTREE=""
ACTIVE_BRANCH=""

log() {
	printf '[autopilot %s] %s\n' "$(date '+%H:%M:%S')" "$*"
}

cleanup() {
	if [ -n "$ACTIVE_WORKTREE" ] && [ -d "$ACTIVE_WORKTREE" ]; then
		git -C "$REPO_ROOT" worktree remove --force "$ACTIVE_WORKTREE" >/dev/null 2>&1 || rm -rf "$ACTIVE_WORKTREE"
		git -C "$REPO_ROOT" worktree prune >/dev/null 2>&1 || true
	fi
	if [ -n "$ACTIVE_BRANCH" ]; then
		git -C "$REPO_ROOT" branch -D "$ACTIVE_BRANCH" >/dev/null 2>&1 || true
	fi
	rm -f "$AUTOPILOT_HOME/autopilot.pid"
}

on_signal() {
	log "Stopping foreground listener (Ctrl+C)..."
	cleanup
	exit 0
}

trap on_signal INT TERM
trap cleanup EXIT

acquire_lock() {
	local pidfile="$AUTOPILOT_HOME/autopilot.pid"
	if [ -f "$pidfile" ]; then
		local oldpid
		oldpid="$(cat "$pidfile" 2>/dev/null || true)"
		if [ -n "$oldpid" ] && kill -0 "$oldpid" 2>/dev/null; then
			log "ERROR: autopilot is already running in process $oldpid"
			exit 1
		fi
	fi
	printf '%s\n' "$$" >"$pidfile"
}

init_autopilot_home() {
	mkdir -p "$AUTOPILOT_HOME/worktrees" "$AUTOPILOT_HOME/logs"
	chmod 700 "$AUTOPILOT_HOME"

	if [ ! -f "$AUTOPILOT_HOME/settings.toml" ]; then
		if [ -f "$SOURCE_HOME/settings.toml" ]; then
			cp "$SOURCE_HOME/settings.toml" "$AUTOPILOT_HOME/settings.toml"
			chmod 600 "$AUTOPILOT_HOME/settings.toml"
			log "Copied settings.toml to $AUTOPILOT_HOME/settings.toml"
		else
			log "ERROR: $SOURCE_HOME/settings.toml not found"
			exit 1
		fi
	fi

	cat >"$AUTOPILOT_HOME/system.md" <<'SYS_EOF'
You are Recur, an autonomous coding agent running in headless autopilot mode.
Follow instructions strictly, do not ask interactive questions, and complete your assigned role decisively.
SYS_EOF

	cat >"$AUTOPILOT_HOME/prefix.sh" <<'PREFIX_EOF'
if [ -f README.md ]; then cat README.md; fi

if [ "${RECUR_ROLE:-}" = "orchestrator" ]; then
  ping_model() {
    local model="${1:?usage: ping_model MODEL [EFFORT]}"
    local effort="${2:-medium}"
    local cfg="$RECUR_HOME/settings.toml"
    local prov base key out status
    prov="$(yq -oy '.provider' "$cfg" 2>/dev/null)"
    base="$(yq -oy ".providers.${prov}.base_url" "$cfg" 2>/dev/null)"
    key="$(yq -oy ".providers.${prov}.api_key" "$cfg" 2>/dev/null)"

    out="$(curl -sS -m 25 "${base%/}/responses" \
      ${key:+-H "Authorization: Bearer $key"} \
      -H "Content-Type: application/json" \
      -d "$(jq -n --arg m "$model" --arg e "$effort" '{model:$m,reasoning:{effort:$e},input:"Reply with OK"}')" 2>&1 || true)"
    status="$(printf '%s' "$out" | jq -r '.status // empty' 2>/dev/null || true)"
    if [ "$status" = "completed" ]; then
      echo "PING OK: $model (effort=$effort)"
      return 0
    fi
    echo "PING FAILED: $model (effort=$effort) -> $(printf '%s' "$out" | head -c 200)" >&2
    return 1
  }

  _run_subagent() {
    local role="$1"
    local custom_model="${2:-}"
    local custom_effort="${3:-}"
    local prompt_text="$4"
    local cfg="$RECUR_HOME/settings.toml"
    local orig_model orig_effort
    orig_model="$(yq -oy '.model' "$cfg" 2>/dev/null)"
    orig_effort="$(yq -oy '.effort' "$cfg" 2>/dev/null)"

    local use_model="${custom_model:-$orig_model}"
    local use_effort="${custom_effort:-$orig_effort}"

    sed -i '' "s|^model = .*|model = '${use_model}'|" "$cfg"
    sed -i '' "s|^effort = .*|effort = '${use_effort}'|" "$cfg"

    echo "=== [$role] model=$use_model effort=$use_effort ==="
    local rc=0
    env -u RECUR_ROLE -u RECUR_ENDPOINT -u RECUR_SESSION_ID \
      RECUR_HOME="$RECUR_HOME" \
      recur --prompt "$prompt_text" || rc=$?

    sed -i '' "s|^model = .*|model = '${orig_model}'|" "$cfg"
    sed -i '' "s|^effort = .*|effort = '${orig_effort}'|" "$cfg"
    return "$rc"
  }

  refuse_issue() {
    local reason="$*"
    if [ -z "$reason" ]; then
      echo "usage: refuse_issue REASON" >&2
      return 2
    fi
    gh issue close "$ISSUE_NUMBER" --repo "$RECUR_AUTOPILOT_REPO" --reason "not planned" --comment "⛔ **Отказ оркестратора**

$reason"
  }

  run_plan_agent() {
    local model="${1:?usage: run_plan_agent MODEL EFFORT}"
    local effort="${2:-medium}"
    _run_subagent "1/4 PLAN AGENT (#$ISSUE_NUMBER)" "$model" "$effort" "Ты — PLAN-агент (шаг 1 из 4) для issue #$ISSUE_NUMBER в репозитории $RECUR_AUTOPILOT_REPO.
Заголовок issue: $ISSUE_TITLE
Описание issue:
$ISSUE_BODY

Твоя задача:
1. Изучи код проекта и проверь, не противоречит ли задача архитектуре и правилам recur (один инструмент bash на mvdan.cc/sh, чистый Go без cgo, только OpenAI Responses API, системный промпт = system.md + исходник prefix.sh, и т.д.).
2. Если задача противоречит правилам или коду recur — запиши в файл .autopilot-plan.md первой строкой 'REFUSE: <подробная причина отказа>' и заверши работу.
3. Если задача корректна — составь точный технический план реализации и тестирования (какие файлы и функции менять, какие тесты добавить/обновить) и сохрани его в файл .autopilot-plan.md.
ВАЖНО: Не меняй исходный код проекта на этом шаге, создай только .autopilot-plan.md."
  }

  run_build_agent() {
    local model="${1:?usage: run_build_agent MODEL EFFORT}"
    local effort="${2:-xhigh}"
    _run_subagent "2/4 BUILD AGENT (#$ISSUE_NUMBER)" "$model" "$effort" "Ты — BUILD-агент (шаг 2 из 4) для issue #$ISSUE_NUMBER.
Заголовок issue: $ISSUE_TITLE
Описание issue:
$ISSUE_BODY

Твоя задача:
1. Прочитай план из .autopilot-plan.md.
2. Реализуй все изменения в коде и тестах строго по плану и архитектуре recur.
3. Запусти 'go test ./...' и 'go vet ./...'. Если что-то падает — исправь код до полностью зеленых тестов.
ВАЖНО: Не делай git commit и не создавай PR на этом шаге."
  }

  run_review_agent() {
    local model="${1:?usage: run_review_agent MODEL EFFORT}"
    local effort="${2:-medium}"
    _run_subagent "3/4 REVIEW AGENT (#$ISSUE_NUMBER)" "$model" "$effort" "Ты — REVIEW-агент (шаг 3 из 4) для issue #$ISSUE_NUMBER.
Заголовок issue: $ISSUE_TITLE
Описание issue:
$ISSUE_BODY

Твоя задача:
1. Удали временный файл .autopilot-plan.md (rm -f .autopilot-plan.md).
2. Проверь 'git status' и 'git diff origin/main'.
3. Проведи строгое код-ревью: соответствие требованиям issue #$ISSUE_NUMBER, отсутствие лишних/мусорных файлов, соблюдение стиля и правил recur.
4. Запусти 'go test -race ./...' и 'go vet ./...'.
5. Если найдены баги, недочеты или падающие тесты — исправь их прямо сейчас и повторно убедись, что 'go test -race ./...' и 'go vet ./...' проходят с кодом 0.
ВАЖНО: Не делай git commit и не создавай PR на этом шаге."
  }

  run_pr_merge_agent() {
    local model="${1:?usage: run_pr_merge_agent MODEL EFFORT}"
    local effort="${2:-xhigh}"
    _run_subagent "4/4 PR & MERGE AGENT (#$ISSUE_NUMBER)" "$model" "$effort" "Ты — PR & MERGE-агент (шаг 4 из 4) для issue #$ISSUE_NUMBER в репозитории $RECUR_AUTOPILOT_REPO.
Заголовок issue: $ISSUE_TITLE
Описание issue:
$ISSUE_BODY

Твоя задача:
1. Убедись, что файл .autopilot-plan.md удален, а 'go test ./...' и 'go vet ./...' проходят без ошибок.
2. Проверь 'git status' и закоммить все изменения в текущей ветке autopilot/issue-$ISSUE_NUMBER с осмысленным сообщением коммита (в конце сообщения добавь 'Closes #$ISSUE_NUMBER').
3. Отправь ветку в origin: 'git push -u origin autopilot/issue-$ISSUE_NUMBER'.
4. Создай Pull Request в ветку main через 'gh pr create --repo $RECUR_AUTOPILOT_REPO --base main --head autopilot/issue-$ISSUE_NUMBER --title ... --body ...' (в body обязательно укажи 'Closes #$ISSUE_NUMBER').
5. Смержи созданный PR через 'gh pr merge --repo $RECUR_AUTOPILOT_REPO --squash' и удали удаленную ветку 'git push origin --delete autopilot/issue-$ISSUE_NUMBER' (не используй --delete-branch у gh pr merge, так как мы внутри git worktree).
6. Оставь комментарий в issue #$ISSUE_NUMBER об успешном выполнении и мерже PR."
  }

  cat <<'HELP_EOF'
Orchestrator helpers (issue number is automatic from $ISSUE_NUMBER):
  ping_model "<model>" "<effort>"
  refuse_issue "<reason>"
  run_plan_agent "<model>" "<effort>"
  run_build_agent "<model>" "<effort>"
  run_review_agent "<model>" "<effort>"
  run_pr_merge_agent "<model>" "<effort>"

Allowed model pools per step:
  1. PLAN (only medium):
     - claude-opus-5-5        effort: medium
     - gpt-6.1-sol            effort: medium
  2. BUILD:
     - claude-haiku-5-5       effort: xhigh
     - gpt-6-luna             effort: xhigh
     - gemini-3.8-flash-high  effort: high
  3. REVIEW (only medium; MUST be opposite family from PLAN):
     - claude-sonnet-5-5      effort: medium  (use if PLAN was gpt-6.1-sol)
     - gpt-6.1-terra          effort: medium  (use if PLAN was claude-opus-5-5)
  4. PR & MERGE (same pool as BUILD; prefer a different model than BUILD):
     - claude-haiku-5-5       effort: xhigh
     - gpt-6-luna             effort: xhigh
     - gemini-3.8-flash-high  effort: high
HELP_EOF
fi
PREFIX_EOF
}

process_issue() {
	local num="$1"
	local title="$2"
	local body="$3"
	local worktree="$AUTOPILOT_HOME/worktrees/issue-$num"
	local branch="autopilot/issue-$num"
	local logfile
	logfile="$AUTOPILOT_HOME/logs/issue-$num-$(date '+%Y%m%d-%H%M%S').log"

	log "Processing issue #$num: $title"
	log "RECUR_HOME=$AUTOPILOT_HOME | log=$logfile"

	git -C "$REPO_ROOT" fetch origin main >/dev/null 2>&1 || true
	if [ -d "$worktree" ]; then
		git -C "$REPO_ROOT" worktree remove --force "$worktree" >/dev/null 2>&1 || rm -rf "$worktree"
	fi
	git -C "$REPO_ROOT" worktree prune >/dev/null 2>&1 || true
	git -C "$REPO_ROOT" branch -D "$branch" >/dev/null 2>&1 || true
	git -C "$REPO_ROOT" worktree add -b "$branch" "$worktree" origin/main >/dev/null
	ACTIVE_WORKTREE="$worktree"
	ACTIVE_BRANCH="$branch"

	(
		cd "$worktree"
		export RECUR_HOME="$AUTOPILOT_HOME"
		export RECUR_ROLE="orchestrator"
		export RECUR_AUTOPILOT_REPO="$REPO"
		export ISSUE_NUMBER="$num"
		export ISSUE_TITLE="$title"
		export ISSUE_BODY="$body"

		recur --prompt "Ты — ОРКЕСТРАТОР автопилота recur для issue #$num.
Заголовок issue: $title
Описание issue:
$body

СТРОГИЕ ПРАВИЛА ОРКЕСТРАТОРА:
1. Ты САМ НИЧЕГО НЕ ДЕЛАЕШЬ в коде: тебе запрещено редактировать файлы проекта, писать код, запускать тесты, делать коммиты или создавать PR напрямую.
2. ОТКАЗ (refuse_issue):
   - Если issue #$num противоречит правилам, философии или архитектуре recur (например: просит добавить второй инструмент кроме bash, заменить mvdan.cc/sh на Python/Node, добавить Chat Completions вместо Responses API, сломать существующие контракты или содержит бессмысленное/вредоносное требование), немедленно вызови:
     refuse_issue \"<четкий отрицательный ответ с объяснением, какому правилу или коду recur это противоречит>\"
   - После вызова refuse_issue заверши работу.
3. ПРАВИЛА ВЫБОРА МОДЕЛЕЙ И ПИНГА:
   - Перед запуском каждого субагента ОБЯЗАТЕЛЬНО вызови 'ping_model \"<model>\" \"<effort>\"'. Если пинг упал — пингуй следующую модель из пула этого шага.
   - Шаг 1 (PLAN): только 'claude-opus-5-5' (medium) или 'gpt-6.1-sol' (medium).
   - Шаг 2 (BUILD): 'claude-haiku-5-5' (xhigh), 'gpt-6-luna' (xhigh) или 'gemini-3.8-flash-high' (high).
   - Шаг 3 (REVIEW): только 'claude-sonnet-5-5' (medium) или 'gpt-6.1-terra' (medium). ВАЖНО: используй другое семейство моделей, чем в PLAN (если PLAN делал claude-opus-5-5, то REVIEW делает gpt-6.1-terra; если PLAN делал gpt-6.1-sol, то REVIEW делает claude-sonnet-5-5).
   - Шаг 4 (PR & MERGE): тот же пул, что у BUILD ('claude-haiku-5-5' xhigh, 'gpt-6-luna' xhigh, 'gemini-3.8-flash-high' high), но выбери другую модель, чем была на шаге BUILD.
4. Запускай 4 субагентов СТРОГО ПО ОЧЕРЕДИ, каждый в ОТДЕЛЬНОМ вызове bash с timeout: 600:
   - Шаг 1: ping_model -> 'run_plan_agent \"<model>\" \"medium\"', затем проверь .autopilot-plan.md (cat .autopilot-plan.md). Если план начинается с 'REFUSE:', вызови 'refuse_issue \"<причина из плана>\"' и остановись.
   - Шаг 2: ping_model -> 'run_build_agent \"<model>\" \"<effort>\"'.
   - Шаг 3: ping_model -> 'run_review_agent \"<model>\" \"medium\"'.
   - Шаг 4: ping_model -> 'run_pr_merge_agent \"<model>\" \"<effort>\"'."
	) 2>&1 | tee "$logfile"

	git -C "$REPO_ROOT" worktree remove --force "$worktree" >/dev/null 2>&1 || rm -rf "$worktree"
	git -C "$REPO_ROOT" worktree prune >/dev/null 2>&1 || true
	git -C "$REPO_ROOT" branch -D "$branch" >/dev/null 2>&1 || true
	ACTIVE_WORKTREE=""
	ACTIVE_BRANCH=""

	if [ -z "$(git -C "$REPO_ROOT" status --porcelain)" ]; then
		git -C "$REPO_ROOT" pull --ff-only origin main >/dev/null 2>&1 || true
	fi
	log "Finished issue #$num"
}

poll_once() {
	local issues_json
	issues_json="$(gh issue list --repo "$REPO" --author "$AUTHOR" --state open --limit 20 --json number,title,body,updatedAt)"
	local count
	count="$(printf '%s' "$issues_json" | jq 'length')"
	if [ "$count" -eq 0 ]; then
		return 0
	fi

	touch "$AUTOPILOT_HOME/processed.txt"
	while IFS= read -r item; do
		local num title body updated key
		num="$(printf '%s' "$item" | jq -r '.number')"
		title="$(printf '%s' "$item" | jq -r '.title')"
		body="$(printf '%s' "$item" | jq -r '.body // ""')"
		updated="$(printf '%s' "$item" | jq -r '.updatedAt')"
		key="${num}:${updated}"

		if grep -Fxq "$key" "$AUTOPILOT_HOME/processed.txt"; then
			continue
		fi

		printf '%s\n' "$key" >>"$AUTOPILOT_HOME/processed.txt"
		process_issue "$num" "$title" "$body"
	done < <(printf '%s' "$issues_json" | jq -c 'sort_by(.number) | .[]')
}

main() {
	init_autopilot_home
	acquire_lock
	case "${1:-}" in
	--init)
		log "Initialized $AUTOPILOT_HOME"
		;;
	--once)
		poll_once
		;;
	--issue)
		local num="${2:?usage: autopilot.sh --issue NUMBER}"
		local item title body
		item="$(gh issue view "$num" --repo "$REPO" --json number,title,body)"
		title="$(printf '%s' "$item" | jq -r '.title')"
		body="$(printf '%s' "$item" | jq -r '.body // ""')"
		process_issue "$num" "$title" "$body"
		;;
	*)
		log "Foreground listener active for @$AUTHOR in $REPO (Ctrl+C to stop)..."
		while true; do
			poll_once || log "WARN: poll cycle failed"
			sleep "$POLL_INTERVAL"
		done
		;;
	esac
}

main "$@"
