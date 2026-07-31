#!/usr/bin/env bash
# Capture une réponse réelle de l'API PVE dans testdata/, anonymisée.
#
# Rejouer une vraie réponse plutôt qu'une réponse écrite à la main est tout
# l'intérêt : une fixture écrite à la main est d'accord avec ce que le
# développeur croyait du schéma — exactement le mode de défaillance que ce
# projet cherche à éviter (PRD §6.3).
set -euo pipefail

endpoint="${1:?usage: capture.sh /nodes [nom]}"
name="${2:-}"
[ -n "$name" ] || name="$(echo "${endpoint#/}" | tr '/' '-')"

: "${PVE_API_URL:?exporte PVE_API_URL}"
: "${PVE_API_TOKEN_ID:?exporte PVE_API_TOKEN_ID}"
: "${PVE_API_TOKEN_SECRET:?exporte PVE_API_TOKEN_SECRET}"

out="testdata/${name}.json"

curl -sS --fail-with-body \
     --cacert /dev/null --insecure \
     -H "Authorization: PVEAPIToken=${PVE_API_TOKEN_ID}=${PVE_API_TOKEN_SECRET}" \
     "${PVE_API_URL%/}/api2/json${endpoint}" \
| python3 -c '
import json, re, sys

raw = sys.stdin.read()

# Anonymisation : ce qui identifie CE lab ne doit pas partir dans un dépôt public.
raw = re.sub(r"\b(?:\d{1,3}\.){3}\d{1,3}\b", "203.0.113.10", raw)          # RFC 5737
raw = re.sub(r"\b(?:[0-9A-Fa-f]{2}:){5}[0-9A-Fa-f]{2}\b", "AA:BB:CC:DD:EE:FF", raw)
raw = re.sub(r"[0-9A-Fa-f]{2}(?::[0-9A-Fa-f]{2}){31}", "AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66:77:88:99:AA:BB:CC:DD:EE:FF:00:11:22:33:44:55:66:77:88:99", raw)
raw = re.sub(r"[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}", "00000000-0000-0000-0000-000000000000", raw)

# Une cle publique SSH nest pas un secret, mais elle nomme une machine et un
# utilisateur : pas dans un depot public.
raw = re.sub(r"ssh-(?:rsa|ed25519|dss)[%A-Za-z0-9+/=.@-]*", "ssh-ed25519%20AAAA_CLE_PUBLIQUE_ANONYMISEE%20operateur%40poste", raw)

print(json.dumps(json.loads(raw), indent=2, sort_keys=True))
' > "$out"

echo "→ $out"
