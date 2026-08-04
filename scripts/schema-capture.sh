#!/usr/bin/env bash
# Capture le schéma d'API PVE (endpoints, paramètres, types de retour) et
# l'écrit NORMALISÉ dans docs/schema-snapshots/ — PVX-104.
#
# Le bundle brut de l'API viewer (apidoc.js, 3-4 Mo par version) est mis en
# cache dans .schema-cache/ et RÉUTILISÉ s'il est déjà présent (FORCE=1 pour
# forcer un nouveau téléchargement) — jamais versionné : seul le schéma
# normalisé (chemin, méthode, paramètres et types, énumération, format,
# valeur par défaut — rien d'autre, pas de description, pas de HTML) est
# destiné à git.
#
# Dépendances : bash, curl, python3 (déjà utilisé par scripts/capture.sh),
# et dpkg-deb + ar pour le mode « archive » (déjà sur toute machine
# Debian-like). Aucune dépendance ajoutée.
#
# Deux sources, mesurées avant d'écrire ce script :
#
#   node     Le nœud live, en HTTPS, SANS authentification : l'API viewer
#            est un asset statique du serveur web de pveproxy, servi tel
#            quel (vérifié : 200 sans en-tête Authorization). La vérification
#            TLS reste ACTIVE par défaut ; PVE_TLS_INSECURE=1 la désactive
#            explicitement (nœud de lab en certificat auto-signé), avec un
#            avertissement — un outil qui sert de référence de compatibilité
#            ne doit pas accepter n'importe quel certificat en silence.
#
#   archive  Un paquet pve-docs archivé, en HTTP EXPLICITE. En HTTPS,
#            download.proxmox.com répond 401 (il partage IP et certificat
#            avec enterprise.proxmox.com depuis ce réseau — mesuré, pas
#            supposé) ; en HTTP, c'est le protocole documenté par Proxmox
#            lui-même pour ce dépôt (« deb http://download.proxmox.com/... »).
#            L'apidoc.js n'est PAS dans pve-manager (vérifié absent) : il
#            est dans pve-docs, à usr/share/pve-docs/api-viewer/apidoc.js.
#            Le paquet est désarchivé avec dpkg-deb -x dans un répertoire
#            temporaire, jamais installé.
#
# Ces deux scripts (avec schema-diff.sh) ne sont pas encore appelés par la CI
# ni par un test Go — la capture/diff reste un geste manuel jusqu'à la
# tranche 3 (rejeu des fixtures de testdata/ contre deux schémas), qui
# décidera de la bonne intégration.
#
# Code de sortie : toujours 2 en cas d'échec, jamais 1. Une capture ne rend
# pas de « résultat » comparable à celui d'un diff (schema-diff.sh, lui,
# réserve 1 à « au moins un endpoint retiré » — un résultat, pas une panne) ;
# elle réussit (0) ou échoue (2), point.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cache_dir="$repo_root/.schema-cache"
out_dir="$repo_root/docs/schema-snapshots"

# Nombre d'endpoints en dessous duquel une capture est refusée : un extrait
# partiel (mauvais marqueur, réponse tronquée qui aurait échappé aux gardes
# précédents) doit échouer bruyamment plutôt que d'écraser un snapshot
# correct avec un sous-ensemble plausible. 300 est sous le plus petit bundle
# réel observé (PVE 7.4 : 540 endpoints) avec de la marge.
MIN_PLAUSIBLE_ENDPOINTS="${MIN_PLAUSIBLE_ENDPOINTS:-300}"

usage() {
  cat >&2 <<'USAGE'
usage:
  schema-capture.sh node <version> [url]
      Capture le schéma depuis le nœud live. <version> est la version déjà
      connue du nœud (pvecli config show -> detected_version, ou la sortie
      de « pvecli version ») — ce script ne l'interroge pas lui-même pour
      rester utilisable sans identifiant. [url] surcharge $PVE_API_URL
      (même variable que scripts/capture.sh). PVE_TLS_INSECURE=1 désactive
      la vérification TLS (nœud de lab en certificat auto-signé) ; sans
      cette variable, un certificat invalide fait échouer la capture.

  schema-capture.sh archive <version> [suite]
      Capture le schéma depuis un paquet pve-docs archivé, en HTTP.
      <version> : version exacte du paquet, ex. 9.0.4
      [suite]   : suite Debian du dépôt PVE — défaut trixie (PVE 9.x),
                  bookworm pour PVE 8.x.

Variables :
  FORCE=1                    force un nouveau téléchargement même si le
                              bundle brut est déjà en cache.
  MIN_PLAUSIBLE_ENDPOINTS=N  plancher de plausibilité (défaut 300).
USAGE
  exit 2
}

[ $# -ge 2 ] || usage
mode="$1"
version="$2"

case "$version" in
  *[!0-9A-Za-z.~-]*)
    echo "version suspecte (\"$version\") — attendu : chiffres, lettres, points, tirets" >&2
    exit 2
    ;;
esac

case "$mode" in
  node|archive) ;;
  *) usage ;;
esac

mkdir -p "$cache_dir" "$out_dir"

case "$mode" in
  node)
    url="${3:-${PVE_API_URL:-}}"
    if [ -z "$url" ]; then
      echo "exporte PVE_API_URL, ou passe une URL en 3e argument" >&2
      exit 2
    fi
    raw="$cache_dir/${version}-node-apidoc.js"
    source_label="node"
    if [ -s "$raw" ] && [ "${FORCE:-}" != "1" ]; then
      echo "→ bundle déjà en cache (${raw}) — FORCE=1 pour re-télécharger" >&2
    else
      insecure_flag=()
      if [ "${PVE_TLS_INSECURE:-}" = "1" ]; then
        echo "⚠ vérification TLS désactivée (PVE_TLS_INSECURE=1) — accepte n'importe quel certificat" >&2
        insecure_flag=(--insecure)
      fi
      echo "→ capture depuis le nœud (${url%/}/pve-docs/api-viewer/apidoc.js)" >&2
      if ! curl -sS --fail-with-body "${insecure_flag[@]}" \
           "${url%/}/pve-docs/api-viewer/apidoc.js" -o "$raw"; then
        echo "capture depuis le nœud échouée (curl) — vérifie PVE_API_URL, la joignabilité, ou PVE_TLS_INSECURE" >&2
        rm -f "$raw"
        exit 2
      fi
    fi
    ;;
  archive)
    suite="${3:-trixie}"
    # La suite fait partie de la clé de cache, pas seulement la version : un
    # même numéro de version existe dans plusieurs suites, et réutiliser le
    # bundle bookworm pour une demande trixie ferait écrire au snapshot une
    # provenance fausse — sur un fichier versionné dont c'est le rôle
    # d'attester d'où il vient.
    raw="$cache_dir/${version}-archive-${suite}-apidoc.js"
    source_label="archive:pve-docs_${version}_all.deb(${suite})"
    if [ -s "$raw" ] && [ "${FORCE:-}" != "1" ]; then
      echo "→ bundle déjà en cache (${raw}) — FORCE=1 pour re-télécharger" >&2
    else
      command -v dpkg-deb >/dev/null 2>&1 || {
        echo "dpkg-deb introuvable — nécessaire pour désarchiver pve-docs sans l'installer" >&2
        exit 2
      }
      deb_url="http://download.proxmox.com/debian/pve/dists/${suite}/pve-no-subscription/binary-amd64/pve-docs_${version}_all.deb"
      tmp="$(mktemp -d)"
      trap 'rm -rf "$tmp"' EXIT
      echo "→ téléchargement de ${deb_url}" >&2
      if ! curl -sS --fail-with-body "$deb_url" -o "$tmp/pve-docs.deb"; then
        echo "téléchargement du paquet pve-docs échoué (curl) — vérifie la version et la suite" >&2
        exit 2
      fi
      dpkg-deb -x "$tmp/pve-docs.deb" "$tmp/extract"
      extracted="$tmp/extract/usr/share/pve-docs/api-viewer/apidoc.js"
      [ -s "$extracted" ] || {
        echo "apidoc.js absent ou vide dans le paquet pve-docs ${version} (${suite}) — capture refusée" >&2
        exit 2
      }
      cp "$extracted" "$raw"
    fi
    ;;
esac

[ -s "$raw" ] || {
  echo "schéma brut vide (${raw}) — capture refusée, un schéma vide n'est pas un succès" >&2
  exit 2
}

out="$out_dir/${version}-${mode}.json"

python3 - "$raw" "$out" "$version" "$source_label" "$MIN_PLAUSIBLE_ENDPOINTS" <<'PYEOF'
import json, os, re, sys


def fail(msg):
    """Toute impossibilité de capturer sort en 2, jamais en 1 — une capture
    n'a pas de « résultat » à signaler comme le 1 de schema-diff.sh, elle
    réussit ou échoue. `sys.exit("message")` sort en 1 par défaut en
    Python ; cette fonction est ce qui l'évite, partout où ce bloc échoue."""
    print(msg, file=sys.stderr)
    raise SystemExit(2)


raw_path, out_path, version, source, min_plausible = sys.argv[1:6]
min_plausible = int(min_plausible)

with open(raw_path, encoding="utf-8") as f:
    text = f.read()

# Même extraction que le script de recherche existant
# (~/.agents/skills/proxmox-api/scripts/search-pve-api.ts) : le fichier est
# un module JS, pas du JSON pur — on isole le littéral `const apiSchema = [
# ... ]` par comptage de profondeur, en respectant les chaînes, puis on le
# parse comme JSON (le littéral lui-même est un JSON valide).
#
# Le marqueur est ANCRÉ (limite de mot + espace optionnel avant le `=`) :
# un simple `text.index("const apiSchema")` matche aussi `const
# apiSchemaMeta`, ce qui a été mesuré en revue adverse comme rendant un
# succès à 1 endpoint sur 605, exit 0, qui aurait écrasé un snapshot correct.
match = re.search(r"\bconst\s+apiSchema\s*=", text)
if not match:
    fail("apidoc.js : marqueur `const apiSchema =` introuvable — capture refusée (corps non-JS, page d'erreur, ou format inattendu)")

start = text.find("[", match.end())
if start < 0:
    fail("apidoc.js : marqueur trouvé mais aucun tableau à la suite — capture refusée")

depth = 0
in_str = False
esc = False
end = -1
for idx in range(start, len(text)):
    c = text[idx]
    if in_str:
        if esc:
            esc = False
        elif c == "\\":
            esc = True
        elif c == '"':
            in_str = False
        continue
    if c == '"':
        in_str = True
    elif c == "[":
        depth += 1
    elif c == "]":
        depth -= 1
        if depth == 0:
            end = idx + 1
            break

if end < 0:
    fail("apidoc.js : tableau apiSchema tronqué ou mal formé — capture refusée")

try:
    tree = json.loads(text[start:end])
except json.JSONDecodeError as exc:
    fail(f"apidoc.js : tableau apiSchema extrait mais JSON invalide ({exc}) — capture refusée")

# Axes retenus, au-delà du type nu : enum, format et default sont ceux dont
# la revue adverse a mesuré, sur des données PVE réelles (7.4 -> 9.2.6), une
# rupture de contrat invisible sans eux (retrait de `glusterfs` de l'enum de
# `type`, changement de `format` sur `nodes`, etc.). `pattern` est retenu
# pour la même raison qu'il définit la syntaxe acceptée, comme `format`.
#
# `minimum`/`maximum`/`maxLength` sont volontairement EXCLUS : aucune
# rupture réelle sur ces seuls axes n'a été observée dans les jeux de
# données PVE examinés (contrairement à enum/format/default/pattern), et PVE
# encode déjà la plupart des bornes numériques dans `format` (ex.
# `pve-vmid` implique 100-999999999). À revoir si un futur diff prouve
# qu'un changement de borne seule casse un appelant — le même mécanisme
# s'étendrait alors de la même façon.
def field_type(spec):
    """Le type d'un paramètre ou d'un champ de retour, en une seule chaîne
    diffable ligne à ligne — c'est schema-diff.sh qui la redécompose pour
    produire un message par axe, pas ce normaliseur : stocker un objet
    structuré ici (type/optional/enum/format/... en clés séparées) a été
    essayé et mesuré à 745-871 Ko par version (contre ~200-230 Ko en chaîne
    plate), pour un gain de lisibilité nul dans un fichier destiné à être
    diffé, pas parcouru programmatiquement.

    Axes retenus au-delà du type nu — enum, format, pattern, default — parce
    que la revue adverse a mesuré, sur des données PVE réelles (7.4 ->
    9.2.6), des ruptures de contrat invisibles sans eux (retrait de
    `glusterfs` de l'énumération de `type`, changement de `format` sur
    `nodes`, etc.). `minimum`/`maximum`/`maxLength` sont volontairement
    EXCLUS : aucune rupture réelle sur ces seuls axes n'a été observée dans
    les jeux de données examinés, et PVE encode déjà la plupart des bornes
    numériques dans `format` (ex. `pve-vmid` implique 100-999999999). À
    revoir si un futur diff prouve qu'un changement de borne seule casse un
    appelant.
    """
    if not isinstance(spec, dict):
        return "unknown"
    t = spec.get("type", "string")
    if isinstance(t, list):
        t = "|".join(t)
    s = f"{t}?" if spec.get("optional") else str(t)
    if spec.get("enum"):
        s += " enum(" + ",".join(sorted(str(v) for v in spec["enum"])) + ")"
    # `format` est presque toujours un identifiant court (« pve-vmid »).
    # Sur des paramètres composites (ex. `ide[n]`, `net[n]`), PVE y met à la
    # place un OBJET décrivant tout un sous-schéma (des dizaines de champs) —
    # mesuré comme la cause principale de l'explosion de taille ci-dessus.
    # Seul le format SCALAIRE est retenu ; un format composite est un axe de
    # compatibilité réel mais hors du périmètre de ce lot.
    fmt = spec.get("format")
    if isinstance(fmt, str):
        s += f" fmt({fmt})"
    pattern = spec.get("pattern")
    if isinstance(pattern, str):
        s += f" pattern({pattern})"
    if "default" in spec and isinstance(spec["default"], (str, int, float, bool)):
        s += f" def({spec['default']})"
    return s


def returns_shape(returns):
    if not returns:
        return {"kind": "none"}
    t = returns.get("type", "object")
    if t == "array":
        items = returns.get("items") or {}
        props = items.get("properties")
        if props:
            return {"kind": "array", "fields": {k: field_type(v) for k, v in props.items()}}
        return {"kind": "array"}
    if t == "object":
        props = returns.get("properties")
        if props:
            return {"kind": "object", "fields": {k: field_type(v) for k, v in props.items()}}
        return {"kind": "object"}
    return {"kind": t}


endpoints = []


def walk(node):
    info = node.get("info") or {}
    path = node.get("path")
    for method, spec in info.items():
        params = (spec.get("parameters") or {}).get("properties") or {}
        endpoints.append(
            {
                "method": method,
                "path": path,
                "parameters": {k: field_type(v) for k, v in params.items()},
                "returns": returns_shape(spec.get("returns")),
            }
        )
    for child in node.get("children") or []:
        walk(child)


for node in tree:
    walk(node)

if len(endpoints) < min_plausible:
    fail(
        f"seulement {len(endpoints)} endpoints trouvés (< {min_plausible}, plancher de "
        "plausibilité) — capture refusée : un extrait partiel ne doit pas écraser un "
        "snapshot correct. Ajuster MIN_PLAUSIBLE_ENDPOINTS si ce plancher est faux "
        "pour cette version."
    )

endpoints.sort(key=lambda e: (e["path"], e["method"]))

# Pas de captured_at : pour une source « archive », le paquet est immuable et
# l'horodatage ne porte aucune information — il ne produit que du bruit git
# à chaque re-capture (vérifié : re-capturer la même version archivée rend
# un fichier identique octet pour octet sans ce champ).
out = {
    "pve_version": version,
    "source": source,
    "endpoint_count": len(endpoints),
    "endpoints": endpoints,
}

# Écriture atomique : fichier temporaire puis renommage. Une écriture
# interrompue (disque plein, kill) laisse alors le fichier temporaire
# tronqué, jamais le fichier versionné.
# Le temporaire est nettoyé si l'écriture échoue : sans cela il survit dans
# le répertoire versionné et un `git add` du répertoire l'embarquerait.
tmp_out = out_path + ".tmp"
try:
    with open(tmp_out, "w", encoding="utf-8") as f:
        json.dump(out, f, indent=1, sort_keys=True, ensure_ascii=False)
        f.write("\n")
    os.replace(tmp_out, out_path)
except BaseException:
    try:
        os.unlink(tmp_out)
    except OSError:
        pass
    raise

print(f"→ {out_path} ({len(endpoints)} endpoints)", file=sys.stderr)
PYEOF
