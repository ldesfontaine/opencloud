#!/bin/sh
# Le banc du parcours : un serveur neuf, deux machines (la seconde dans un espace
# de noms qui lui donne son propre machine-id et nom d'hôte), un faux Docker, un
# site de test et un récepteur de webhook, le tout en services `systemd --user`.
# Rien de root, rien de la production. Usage : banc.sh up | down
set -eu
HERE=$(cd "$(dirname "$0")" && pwd)
ROOT=$(cd "$HERE/../../../.." && pwd)
WORK=${OC_WORK:-$HOME/oc-banc}
RUN=/run/user/$(id -u)/oc-audit            # socket Unix : chemin court obligatoire
PORT=${OC_PORT:-18480}
BIN=$ROOT/bin/opencloud

case "${1:-}" in
up)
  mkdir -p "$WORK/state" "$WORK/agent" "$WORK/agent2" "$RUN/site"
  chmod 700 "$WORK/state" "$WORK/agent" "$WORK/agent2"
  echo '<h1>demo-shop</h1>' > "$RUN/site/index.html"
  printf 'listen = "127.0.0.1:%s"\nstate_dir = "%s/state"\n' "$PORT" "$WORK" > "$WORK/config.toml"
  echo 11111111111111111111111111111111 > "$RUN/machine-id"
  tr 'a-f' 'b-f' < /proc/sys/kernel/random/boot_id > "$RUN/boot_id"
  cat > "$RUN/lyon.sh" <<LYON
#!/bin/sh
mount --bind $RUN/machine-id /etc/machine-id && mount --bind $RUN/boot_id /proc/sys/kernel/random/boot_id && hostname vps-lyon
exec $BIN agent "\$@" -state $WORK/agent2 -docker-socket $RUN/docker2.sock
LYON
  chmod +x "$RUN/lyon.sh"
  systemd-run --user --quiet --unit=oc-audit-server --collect "$BIN" serve -config "$WORK/config.toml"
  systemd-run --user --quiet --unit=oc-audit-fakedocker --collect node "$HERE/fake-docker.mjs" "$RUN/docker.sock" demo-shop
  systemd-run --user --quiet --unit=oc-audit-fakedocker2 --collect node "$HERE/fake-docker.mjs" "$RUN/docker2.sock" demo-shop
  systemd-run --user --quiet --unit=oc-audit-site --collect python3 -m http.server 18481 --bind 127.0.0.1 --directory "$RUN/site"
  systemd-run --user --quiet --unit=oc-audit-sink --collect node "$HERE/webhook-sink.mjs" 18483 "$RUN/webhook.log"
  ;;
agent)   # banc.sh agent local|lyon <jeton> <droits>
  case "$2" in
  local) printf '%s\n' "$3" | "$BIN" agent -server "http://127.0.0.1:$PORT" -rights "$4" -state "$WORK/agent" -docker-socket "$RUN/docker.sock" 2>&1 | head -n 6 &
         sleep 4; kill %1 2>/dev/null || true
         systemd-run --user --quiet --unit=oc-audit-agent --collect "$BIN" agent -state "$WORK/agent" -docker-socket "$RUN/docker.sock" ;;
  lyon)  printf '%s\n' "$3" | timeout 5 unshare --user --map-root-user --mount --uts "$RUN/lyon.sh" -server "http://127.0.0.1:$PORT" -rights "$4" | head -n 3 || true
         systemd-run --user --quiet --unit=oc-audit-agent2 --collect unshare --user --map-root-user --mount --uts "$RUN/lyon.sh" ;;
  esac ;;
down)
  for unit in server fakedocker fakedocker2 site sink agent agent2; do systemctl --user stop "oc-audit-$unit" 2>/dev/null || true; done
  rm -rf "$WORK" "$RUN"
  ;;
esac
