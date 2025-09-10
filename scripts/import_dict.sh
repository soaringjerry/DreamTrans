#!/usr/bin/env bash
set -euo pipefail

# Usage: ./scripts/import_dict.sh /path/to/EnWords.csv ./backend/data/dict.db

CSV_PATH=${1:-}
OUT_PATH=${2:-./backend/data/dict.db}

if [[ -z "$CSV_PATH" ]]; then
  echo "Usage: $0 /path/to/EnWords.csv [out_dict_db]" >&2
  exit 1
fi

mkdir -p "$(dirname "$OUT_PATH")"

echo "Importing dictionary from $CSV_PATH to $OUT_PATH ..."
go run ./backend/cmd/tools/dict-import -in "$CSV_PATH" -out "$OUT_PATH"
echo "Done. Set DICT_DB_PATH to $OUT_PATH when running backend."

