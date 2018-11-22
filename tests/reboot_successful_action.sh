#! /bin/bash
mkdir /tmp/goahead/
echo "Successful reboot for FQDN ${1} in goahead cluster ${2}" > /tmp/goahead/${2}-${1}-successful-reboot
