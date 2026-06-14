#!/usr/bin/env bash
# main.sh — entry point.

source lib.sh

readonly MAX_RETRIES=3
name="world"

greet
echo "done $name $MAX_RETRIES"
ls /tmp
