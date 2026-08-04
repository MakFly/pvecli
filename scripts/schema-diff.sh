#!/usr/bin/env bash
# Diff deux schémas normalisés produits par schema-capture.sh — PVX-104.
#
# Sortie lisible par un humain sur stdout : endpoints ajoutés, endpoints
# retirés, et pour chaque endpoint modifié, un message PAR AXE — type,
# devenu obligatoire/optionnel, énumération changée (valeurs ajoutées et
# retirées), format changé, valeur par défaut changée. Un diff qui ne
# compare que le type nu rate les ruptures réelles (mesuré en revue adverse :
# retrait de `glusterfs` de l'énumération de `type` sur `POST /storage` et
# `GET /storage`, invisible si seul le type est comparé).
#
# Code de sortie — convention diff(1)/grep(1), tenue partout dans ce script
# et vérifiée cas par cas (pas supposée) :
#   0  comparaison faite, aucun endpoint retiré.
#   1  comparaison faite, au moins un endpoint retiré. C'est un RÉSULTAT,
#      pas une panne — une découverte.
#   2  la comparaison n'a PAS pu se faire : argument manquant, fichier
#      absent, JSON illisible, schéma sans endpoint. Une erreur, jamais
#      confondue avec le 1 ci-dessus.
set -euo pipefail

if [ $# -lt 2 ]; then
  echo "usage: schema-diff.sh ancien.json nouveau.json" >&2
  exit 2
fi
old="$1"
new="$2"

[ -f "$old" ] || { echo "introuvable : $old" >&2; exit 2; }
[ -f "$new" ] || { echo "introuvable : $new" >&2; exit 2; }

python3 - "$old" "$new" <<'PYEOF'
import json, sys


def fail(msg):
    """Toute impossibilité de comparer sort en 2, jamais en 1 : le 1 est
    réservé au résultat « au moins un endpoint retiré », pas aux pannes.
    `sys.exit("message")` sort en 1 par défaut en Python — c'est précisément
    ce que cette fonction évite, partout où ce script peut échouer."""
    print(msg, file=sys.stderr)
    raise SystemExit(2)


old_path, new_path = sys.argv[1:3]


def load(path):
    try:
        with open(path, encoding="utf-8") as f:
            data = json.load(f)
    except json.JSONDecodeError as exc:
        fail(f"{path} : JSON illisible ({exc}) — comparaison impossible")
    if not data.get("endpoints"):
        fail(f"{path} : schéma sans endpoint — rien à comparer, refus de rendre un diff vide")
    by_key = {(e["path"], e["method"]): e for e in data["endpoints"]}
    return data, by_key


old_doc, old_map = load(old_path)
new_doc, new_map = load(new_path)


# Les champs restent stockés comme une seule chaîne diffable (voir
# field_type() dans schema-capture.sh — un format structuré a été mesuré à
# 2-3x la taille pour zéro gain de lisibilité). Ce diff la redécompose ici,
# au moment de la comparaison, par un découpage POSITIONNEL simple — pas une
# regex monolithique qui échouerait silencieusement sur une valeur exotique
# (un pattern regex peut contenir n'importe quel caractère, y compris des
# parenthèses). Le pire cas d'une valeur inattendue est un message générique
# « contrat changé » avec la chaîne brute avant/après affichée telle quelle
# — jamais un crash, jamais une conclusion fausse.
def split_type_token(s):
    """« string? » -> (« string », True). « integer » -> (« integer », False)."""
    tok = s.split(" ", 1)[0]
    if tok.endswith("?"):
        return tok[:-1], True
    return tok, False


def extract_enum(rest):
    """Isole le segment « enum(...) » d'une queue de field_type et rend
    (valeurs, reste_sans_enum), ou (None, rest) s'il n'y en a pas.

    Le découpage compte les parenthèses au lieu de couper à la première
    fermante : 96 `pattern` du schéma PVE en contiennent une, et une regex
    naïve les couperait au mauvais endroit."""
    i = rest.find("enum(")
    if i < 0:
        return None, rest
    depth = 0
    for j in range(i + 4, len(rest)):
        if rest[j] == "(":
            depth += 1
        elif rest[j] == ")":
            depth -= 1
            if depth == 0:
                inner = rest[i + 5:j]
                return inner.split(","), (rest[:i] + rest[j + 1:]).strip()
    return None, rest


def compact(values, limit=300):
    """Liste tronquée : le delta d'une énumération de points de montage LXC
    peut faire près de 4 000 caractères, illisible en revue."""
    joined = ",".join(values)
    if len(joined) <= limit:
        return joined
    kept, used = [], 0
    for v in values:
        if used + len(v) + 1 > limit:
            break
        kept.append(v)
        used += len(v) + 1
    return ",".join(kept) + f"… (+{len(values) - len(kept)} autres)"


def ellipsis(s, limit=200):
    """Chaîne field_type tronquée pour l'affichage d'un champ ajouté ou
    retiré. Le champ `api` d'ACME liste plus de 200 fournisseurs sur une
    seule ligne : la queue n'apprend rien de plus que le début."""
    return s if len(s) <= limit else s[:limit] + f"… (+{len(s) - limit} car.)"


def diff_fields(old_fields, new_fields, label, lines):
    """Compare deux dicts {nom: chaîne field_type} et ajoute un message par
    endpoint modifié — type changé, devenu obligatoire/optionnel affichés
    séparément quand c'est tout ce qui a changé ; sinon la chaîne complète
    avant/après (enum, format, pattern, default) est affichée en clair,
    plutôt que redécomposée axe par axe de façon fragile."""
    old_names, new_names = set(old_fields), set(new_fields)

    for name in sorted(new_names - old_names):
        lines.append(f"    {label} ajouté  : {name} ({ellipsis(new_fields[name])})")
    for name in sorted(old_names - new_names):
        lines.append(f"    {label} retiré  : {name} ({ellipsis(old_fields[name])})")

    for name in sorted(old_names & new_names):
        a, b = old_fields[name], new_fields[name]
        if a == b:
            continue

        a_base, a_opt = split_type_token(a)
        b_base, b_opt = split_type_token(b)
        a_rest = a[len(a.split(" ", 1)[0]):].strip()
        b_rest = b[len(b.split(" ", 1)[0]):].strip()

        if a_base != b_base:
            lines.append(f"    {label} type changé      : {name} {a_base} -> {b_base}")
        if a_opt != b_opt:
            state = "optionnel" if b_opt else "obligatoire"
            lines.append(f"    {label} devenu {state:<11}: {name}")
        if a_rest != b_rest:
            # Quand SEULE l'énumération bouge, on montre ce qui entre et ce
            # qui sort. Relire quatorze valeurs identiques de part et d'autre
            # pour repérer que « glusterfs » a disparu n'aide personne.
            a_enum, a_other = extract_enum(a_rest)
            b_enum, b_other = extract_enum(b_rest)
            gained = sorted(set(b_enum or []) - set(a_enum or []))
            lost = sorted(set(a_enum or []) - set(b_enum or []))

            if a_enum is not None and b_enum is not None and a_other == b_other and (gained or lost):
                lines.append(f"    {label} énumération      : {name}")
                if gained:
                    lines.append(f"        valeurs ajoutées : {compact(gained)}")
                if lost:
                    lines.append(f"        valeurs retirées : {compact(lost)}")
            else:
                lines.append(f"    {label} contrat changé   : {name}")
                lines.append(f"        ancien  : {a}")
                lines.append(f"        nouveau : {b}")


old_keys = set(old_map)
new_keys = set(new_map)

added = sorted(new_keys - old_keys)
removed = sorted(old_keys - new_keys)
common = sorted(old_keys & new_keys)

changed = []
for key in common:
    a, b = old_map[key], new_map[key]
    if a["parameters"] != b["parameters"] or a["returns"] != b["returns"]:
        changed.append(key)

print(
    f"Comparaison de schéma PVE : {old_doc['pve_version']} ({old_doc['source']}) "
    f"-> {new_doc['pve_version']} ({new_doc['source']})"
)
print(f"  {old_doc['endpoint_count']} endpoints -> {new_doc['endpoint_count']} endpoints")
print()

print(f"Endpoints ajoutés ({len(added)}) :")
for path, method in added:
    print(f"  {method:<6} {path}")
if not added:
    print("  (aucun)")
print()

print(f"Endpoints retirés ({len(removed)}) :")
for path, method in removed:
    print(f"  {method:<6} {path}")
if not removed:
    print("  (aucun)")
print()

print(f"Endpoints modifiés ({len(changed)}) :")
if not changed:
    print("  (aucun)")
for path, method in changed:
    a, b = old_map[(path, method)], new_map[(path, method)]
    lines = []
    diff_fields(a["parameters"], b["parameters"], "paramètre", lines)
    ar, br = a["returns"], b["returns"]
    if ar.get("kind") != br.get("kind"):
        lines.append(f"    retour type changé  : {ar.get('kind')} -> {br.get('kind')}")
    diff_fields(ar.get("fields", {}), br.get("fields", {}), "retour champ", lines)
    if lines:
        print(f"  {method} {path}")
        for line in lines:
            print(line)

unchanged = len(common) - len(changed)
print()
print(f"Résumé : {len(added)} ajoutés, {len(removed)} retirés, {len(changed)} modifiés, {unchanged} inchangés")

if removed:
    sys.exit(1)
PYEOF
