#!/bin/sh
# Three servers from one command, each on the port run-stack assigned.

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

mkdir "$work/web" "$work/api"
echo "web of $RUN_STACK_ID, at $APP_URL" > "$work/web/index.html"
echo "api of $RUN_STACK_ID, reached by the web server at $API_URL" > "$work/api/index.html"

python3 -m http.server "$WEB_PORT" -d "$work/web" &
python3 -m http.server "$API_PORT" -d "$work/api" &
python3 -m http.server "$SIDECAR_PORT" -d "$work/api" &
wait
