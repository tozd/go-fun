#!/bin/sh -e

for dir in "testdata/expected-${CONFIG_NAME}"*; do
  echo "Diffing $dir"
  if diff -aur --color=always "$dir/" results/; then
    exit 0
  fi
done

echo "All expected dirs failed diff"
exit 1
