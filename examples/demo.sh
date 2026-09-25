#!/bin/sh
# Two workers, each green on its own, red together. Watch the gate stop the
# second one before it reaches main.
set -eu
if [ -z "${FLEET:-}" ]; then
  FLEET="$(mktemp -d)/fleet"
  (cd "$(dirname "$0")/.." && go build -o "$FLEET" ./cmd/fleet)
fi
demo=$(mktemp -d)
cd "$demo"
git init -q -b main
git config user.name demo && git config user.email demo@example.com
cat > fleet.json <<'JSON'
{
  "mainline": "main",
  "run_dir": ".fleet",
  "agent": ["sh", "-c", "sh \"$FLEET_PROMPT\""],
  "checks": [
    {"name": "port-unique", "run": "test $(cat config/*.port 2>/dev/null | sort | uniq -d | wc -l) -eq 0", "kind": "strict", "timeout_sec": 30}
  ],
  "max_workers": 4,
  "stale_after_min": 30,
  "done_marker": "WORKER-DONE",
  "report_dir": "reports",
  "placeholder_patterns": ["TODO: fill in"]
}
JSON
mkdir config && echo 8080 > config/web.port
git add -A && git commit -qm init

worker() { # branch service port
  cat > "/tmp/fleet-demo-$1.sh" <<SH
mkdir -p config reports
echo $3 > config/$2.port
echo "$2 now listens on $3" > reports/$1.md
git add -A && git commit -qm "add $2 on port $3"
echo "WORKER-DONE $1"
SH
  $FLEET launch --grace 0 "$1" "/tmp/fleet-demo-$1.sh"
}
worker add-metrics metrics 9090
worker add-admin admin 9090
sleep 1

echo; $FLEET ledger
echo; echo '$ fleet harvest add-metrics'; $FLEET harvest add-metrics || true
echo; echo '$ fleet harvest add-admin';   $FLEET harvest add-admin || true
echo; echo "main is at: $(git log --oneline -1)"
echo "demo repo: $demo"
