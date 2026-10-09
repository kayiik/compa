#!/bin/sh
set -eu

export COMPA_HOME="${COMPA_HOME:-/data}"
export DISPLAY="${DISPLAY:-:99}"
export PLAYWRIGHT_BROWSERS_PATH="${PLAYWRIGHT_BROWSERS_PATH:-/ms-playwright}"
export HOME="${COMPA_BROWSER_HOME:-$COMPA_HOME/browser-home}"
PROFILE_DIR="${COMPA_BROWSER_PROFILE:-$COMPA_HOME/browser-profile}"
OUTPUT_DIR="${COMPA_BROWSER_OUTPUT:-$COMPA_HOME/browser-output}"
LOG_DIR="${COMPA_BROWSER_LOG_DIR:-$COMPA_HOME}"
LOG_FILE="${COMPA_BROWSER_LOG_FILE:-$LOG_DIR/browser-services.log}"

mkdir -p "$HOME" "$PROFILE_DIR" "$OUTPUT_DIR" "$LOG_DIR"
umask 077
touch "$LOG_FILE"

if [ "$#" -eq 0 ]; then
  set -- compa-kernel
fi

Xvfb "$DISPLAY" -screen 0 1280x800x24 -ac -nolisten tcp >>"$LOG_FILE" 2>&1 &

wait_for_port() {
  host="$1"
  port="$2"
  name="$3"
  pid="$4"
  node - "$host" "$port" "$name" "$pid" <<'NODE'
const net = require('net');
const [host, port, name, pid] = process.argv.slice(2);
const deadline = Date.now() + 5000;
const tryConnect = () => {
  const socket = net.createConnection({ host, port: Number(port) });
  socket.setTimeout(500);
  socket.on('connect', () => {
    socket.end();
    process.exit(0);
  });
  const retry = () => {
    socket.destroy();
    try {
      process.kill(Number(pid), 0);
    } catch {
      console.error(`${name} exited before becoming ready`);
      process.exit(1);
    }
    if (Date.now() >= deadline) {
      console.error(`${name} did not become ready on ${host}:${port}`);
      process.exit(1);
    }
    setTimeout(tryConnect, 125);
  };
  socket.on('error', retry);
  socket.on('timeout', retry);
};
tryConnect();
NODE
}

x11vnc -display "$DISPLAY" -rfbport 5900 -listen 127.0.0.1 -nopw -forever -shared >>"$LOG_FILE" 2>&1 &
X11VNC_PID=$!
wait_for_port 127.0.0.1 5900 x11vnc "$X11VNC_PID"

websockify --web=/usr/share/novnc/ 0.0.0.0:6080 127.0.0.1:5900 >>"$LOG_FILE" 2>&1 &
WEBSOCKIFY_PID=$!
wait_for_port 127.0.0.1 6080 websockify "$WEBSOCKIFY_PID"

exec "$@"
