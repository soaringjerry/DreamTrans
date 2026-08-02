#!/usr/bin/env python3
"""Build learning domain term packs from public data (not hand-curated lists).

Sources:
  - JuanitoFatas/Computer-Science-Glossary (dict.textile)
  - skywind3000/ECDICT (CSV with domain tags such as [计]/[心]/[生]/[地])

Usage (from repo root or frontend/):
  python3 frontend/scripts/build_domain_terms.py \\
    --ecdict /path/to/ecdict.csv \\
    --cs /path/to/dict.textile \\
    --out frontend/src/learning/data/domain_terms.json
"""

from __future__ import annotations

import argparse
import csv
import json
import re
from pathlib import Path


def first_zh(trans: str) -> str:
    if not trans:
        return ""
    text = trans.replace("\\n", "\n")
    for line in text.split("\n"):
        line = line.strip()
        if not line:
            continue
        line = re.sub(r"^(?:[a-z]{1,5}\.\s*)+", "", line, flags=re.I)
        line = re.sub(r"\[[^\]]*\]", "", line)
        line = re.sub(r"【[^】]*】", "", line)
        if not re.search(r"[\u4e00-\u9fff]", line):
            continue
        for sep in ["；", ";", "。"]:
            if sep in line:
                line = line.split(sep)[0]
                break
        if "，" in line:
            line = line.split("，")[0]
        line = re.sub(r"（[^）]*）", "", line)
        line = re.sub(r"\([^)]*\)", "", line)
        line = line.strip(" \t.,;，。；、")
        line = re.sub(r"[A-Za-z]{2,}", "", line).strip(" \t.,;，。；、")
        chars: list[str] = []
        for ch in line:
            chars.append(ch)
            if sum(1 for c in chars if "\u4e00" <= c <= "\u9fff") >= 10:
                break
        out = "".join(chars).strip("；、，")
        if re.search(r"[\u4e00-\u9fff]", out):
            return out
    return ""


def normalize_en(en: str) -> str:
    en = en.strip().lower().replace("’", "'")
    return re.sub(r"\s+", " ", en).strip(" .,;:()[]{}")


def ok_term(en: str) -> bool:
    if not en or len(en) < 2 or len(en) > 48:
        return False
    if not re.fullmatch(r"[a-z0-9][a-z0-9\s\-'/]*", en):
        return False
    if len(en.split()) > 5:
        return False
    if en in {"the", "and", "or", "of", "to", "in", "on", "for", "a", "an", "with", "from"}:
        return False
    return True


def parse_cs_glossary(path: Path) -> dict[str, str]:
    terms: dict[str, str] = {}
    for line in path.read_text(encoding="utf-8", errors="replace").splitlines():
        line = line.strip()
        if not line.startswith("|") or "英文" in line or "译法" in line:
            continue
        if re.match(r"^\|[\s\-:|]+\|$", line):
            continue
        cells = [c.strip() for c in line.strip("|").split("|")]
        if len(cells) < 2:
            continue
        en_raw = re.sub(r"\*+", "", cells[0]).strip()
        if not en_raw or en_raw.startswith('"') or "开头" in en_raw:
            continue
        zh = ""
        for cell in cells[1:]:
            cell = re.sub(r"\*+", "", cell).strip()
            if cell and re.search(r"[\u4e00-\u9fff]", cell):
                zh = first_zh(cell) or re.split(r"[，;]", cell)[0].strip()
                zh = re.sub(r"[A-Za-z]{2,}", "", zh).strip(" ，,;")
                if re.search(r"[\u4e00-\u9fff]", zh):
                    break
        if not zh:
            continue
        for part in re.split(r"[,，]", en_raw):
            part = re.sub(r"\([^)]*\)", "", part).strip()
            key = normalize_en(part)
            if ok_term(key) and key not in terms:
                terms[key] = zh[:12]
    return terms


def put(
    store: dict[str, dict[str, tuple[str, int]]],
    domain: str,
    key: str,
    zh: str,
    priority: int,
) -> None:
    current = store[domain].get(key)
    if current is None or priority < current[1]:
        store[domain][key] = (zh[:12], priority)


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--ecdict", type=Path, required=True)
    parser.add_argument("--cs", type=Path, required=True)
    parser.add_argument("--out", type=Path, required=True)
    args = parser.parse_args()

    cs_terms = parse_cs_glossary(args.cs)
    print(f"CS glossary terms: {len(cs_terms)}")

    ai_en = re.compile(
        r"(neural|deep learning|machine learning|transformer|embedding|"
        r"language model|reinforcement|supervised|unsupervised|tokenizer|"
        r"backprop|gradient|nlp|computer vision|diffusion|hallucination|"
        r"prompt|fine.?tun)",
        re.I,
    )
    net_en = re.compile(
        r"(http|tcp|udp|dns|cdn|websocket|oauth|saas|microservice|"
        r"kubernetes|docker|devops|frontend|backend|webhook|bandwidth|"
        r"firewall|proxy|load balanc)",
        re.I,
    )

    pri: dict[str, dict[str, tuple[str, int]]] = {
        "ai": {},
        "internet": {},
        "psychology": {},
        "geography": {},
        "biology": {},
    }
    for key, zh in cs_terms.items():
        put(pri, "ai", key, zh, 0)
        put(pri, "internet", key, zh, 0)

    with args.ecdict.open(newline="", encoding="utf-8", errors="replace") as handle:
        for row in csv.DictReader(handle):
            word = normalize_en(row.get("word") or "")
            if not ok_term(word):
                continue
            trans = row.get("translation") or ""
            if any(marker in trans for marker in ("[人名]", "[姓氏]", "[地名]")):
                continue
            zh = first_zh(trans)
            if not zh:
                continue
            tags = set(re.findall(r"\[[^\]]{1,12}\]", trans))
            priority = 1 if " " in word else 2

            if "[心]" in tags or "心理学" in trans:
                put(pri, "psychology", word, zh, priority)
            if tags & {"[生]", "[植]", "[动]", "[生化]", "[解]", "[微]"} or any(
                token in trans for token in ("生物学", "遗传学")
            ):
                put(pri, "biology", word, zh, priority)
            if tags & {"[地质]", "[地]", "[气象]", "[矿]"} or any(
                token in trans for token in ("地理学", "地质学", "气象学")
            ):
                put(pri, "geography", word, zh, priority)

            if (
                "[计]" in tags
                or ai_en.search(word)
                or any(
                    token in trans
                    for token in ("人工智能", "机器学习", "神经网络", "深度学习")
                )
            ):
                if (
                    priority == 1
                    or ai_en.search(word)
                    or any(
                        token in trans
                        for token in (
                            "人工智能",
                            "机器学习",
                            "神经网络",
                            "深度学习",
                            "自然语言处理",
                        )
                    )
                ):
                    put(pri, "ai", word, zh, priority)

            if (
                "[网络]" in tags
                or net_en.search(word)
                or "互联网" in trans
                or "万维网" in trans
            ):
                put(pri, "internet", word, zh, priority)
            elif (
                "[计]" in tags
                and priority == 1
                and re.search(
                    r"network|protocol|server|client|web|http|internet|browser|socket",
                    word + trans,
                    re.I,
                )
            ):
                put(pri, "internet", word, zh, priority)

    caps = {
        "ai": 2000,
        "internet": 2000,
        "biology": 2200,
        "geography": 1800,
        "psychology": 1200,
    }
    labels = {
        "ai": "人工智能 / 计算机",
        "internet": "互联网 / 网络",
        "psychology": "心理学",
        "geography": "地理 / 地球科学",
        "biology": "生物",
    }
    domains_out = {}
    for domain, items in pri.items():
        ordered = sorted(
            items.items(),
            key=lambda item: (
                item[1][1],
                -item[0].count(" "),
                -len(item[0]),
                item[0],
            ),
        )
        terms = {key: value[0] for key, value in ordered[: caps[domain]]}
        domains_out[domain] = {
            "label": labels[domain],
            "terms": dict(sorted(terms.items())),
        }
        print(domain, len(terms))

    payload = {
        "domains": domains_out,
        "sources": {
            "ai": [
                "JuanitoFatas/Computer-Science-Glossary",
                "skywind3000/ECDICT ([计]/AI keywords)",
            ],
            "internet": [
                "JuanitoFatas/Computer-Science-Glossary",
                "skywind3000/ECDICT ([网络]/network keywords)",
            ],
            "psychology": ["skywind3000/ECDICT ([心]/心理学)"],
            "geography": ["skywind3000/ECDICT ([地]/[地质]/[气象]…)"],
            "biology": ["skywind3000/ECDICT ([生]/[植]/[动]/[生化]/[解]…)"],
        },
        "generated": (
            "Automated from public glossaries/dictionaries; "
            "not hand-written term lists."
        ),
    }
    args.out.parent.mkdir(parents=True, exist_ok=True)
    args.out.write_text(
        json.dumps(payload, ensure_ascii=False, separators=(",", ":")),
        encoding="utf-8",
    )
    print("wrote", args.out, "bytes", args.out.stat().st_size)


if __name__ == "__main__":
    main()
