# Learning word packs — attribution

## CEFR levels (`data/learning_pack.json`)

Hard-word CEFR bands are derived from:

- **CEFR-J Vocabulary Profile (ver 1.5)**  
  Compiled by Yukio Tono, Tokyo University of Foreign Studies.  
  Packaged via [openlanguageprofiles/olp-en-cefrj](https://github.com/openlanguageprofiles/olp-en-cefrj).  
  Research and commercial use with proper citation.

Short Chinese glosses for matching lemmas are abbreviated from ECDICT-style
dictionary fields for on-device lookup only.

## Domain terminology (`data/domain_terms.json`)

Built **automatically** by `scripts/build_domain_terms.py` (not hand-written):

| Domain | Sources |
|--------|---------|
| AI / 计算机 | [JuanitoFatas/Computer-Science-Glossary](https://github.com/JuanitoFatas/Computer-Science-Glossary) + [skywind3000/ECDICT](https://github.com/skywind3000/ECDICT) (`[计]` / AI keywords) |
| 互联网 | Same CS glossary + ECDICT (`[网络]` / network keywords) |
| 心理学 | ECDICT (`[心]` / 心理学) |
| 地理 | ECDICT (`[地]` / `[地质]` / `[气象]` …) |
| 生物 | ECDICT (`[生]` / `[植]` / `[动]` / `[生化]` / `[解]` …) |

Rebuild (requires local copies of the source files):

```bash
python3 frontend/scripts/build_domain_terms.py \
  --ecdict /path/to/ecdict.csv \
  --cs /path/to/Computer-Science-Glossary/dict.textile \
  --out frontend/src/learning/data/domain_terms.json
```

Runtime learning assist is local-only (no network / no LLM for gloss lookup).
