#!/bin/bash -e

proxy -v=2 -har -api localhost 2>proxy.log &
PROXY_PID=$!

cleanup() {
  export -n HTTP_PROXY
  export -n http_proxy
  curl -s http://localhost:8181/logs > proxy.har
  kill "$PROXY_PID"
}
trap cleanup EXIT

export HTTP_PROXY=http://localhost:8080
export http_proxy=http://localhost:8080
"$@"
