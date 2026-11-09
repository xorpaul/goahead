#! /bin/bash
DIR="${GOAHEAD_TEST_DIR:-/tmp/goahead}"
mkdir -p "$DIR"
echo "Successful reboot for FQDN ${1} in goahead cluster ${2}" > "$DIR/${2}-${1}-successful-reboot"
