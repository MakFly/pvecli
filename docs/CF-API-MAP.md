# Carte des endpoints Cloudflare

Même règle que pour `docs/API-MAP.md` : **aucun chemin écrit de mémoire**.
Chaque ligne ci-dessous correspond à une entrée de `AllEndpoints`
(`internal/cf/endpoints.go`), et `TestEveryEndpointIsDocumented` échoue sur tout
endpoint absent de ce tableau — ou sur toute ligne de ce tableau qui ne
correspond à aucun endpoint.

Base : `https://api.cloudflare.com/client/v4`

| Endpoint | Méthode | Commande | Ce qu'il faut savoir |
| --- | --- | --- | --- |
| `/user/tokens/verify` | GET | `pvecli cf status` | Vérifie le jeton **avant** toute écriture. Un jeton sans `Cloudflare Tunnel:Edit` échoue sinon au milieu d'une création. |
| `/accounts/{account}/cfd_tunnel` | GET | `cf tunnel ls`, résolution par nom | Renvoie aussi les tunnels **supprimés** (`deleted_at` non nul) — le client les filtre. |
| `/accounts/{account}/cfd_tunnel` | POST | `cf tunnel create` | `config_src: "cloudflare"` = tunnel *remotely-managed* : la table d'ingress vit dans l'API, pas dans un `config.yml` de l'invité. |
| `/accounts/{account}/cfd_tunnel/{tunnel}` | DELETE | `cf tunnel rm` | Refusé tant qu'un connecteur est rattaché. Ce refus est une information. |
| `/accounts/{account}/cfd_tunnel/{tunnel}/token` | GET | `cf tunnel create` | Le jeton de connecteur. **Secret** : rangé au trousseau, jamais affiché ni passé en argument. |
| `/accounts/{account}/cfd_tunnel/{tunnel}/configurations` | GET | `cf route ls`, pré-lecture de `route add/rm` | La réponse enveloppe la table dans `{"config": {"ingress": [...]}}`. |
| `/accounts/{account}/cfd_tunnel/{tunnel}/configurations` | PUT | `cf route add`, `cf route rm` | **Remplace** la table entière. Le client la relit, la modifie, la renvoie — jamais un PUT partiel. |
| `/zones` | GET | résolution de zone | Le suffixe le plus long gagne : avec `example.com` et `lab.example.com`, un nom en `.lab.example.com` appartient à la seconde. |
| `/zones/{zone}/dns_records` | GET | `cf route add/rm` | Filtré par `name` : sert à distinguer une création d'une mise à jour. |
| `/zones/{zone}/dns_records` | POST | `cf route add` | CNAME vers `{tunnel}.cfargotunnel.com`, **toujours** `proxied: true`. |
| `/zones/{zone}/dns_records/{record}` | PUT | `cf route add` | Seulement si l'enregistrement existant pointe déjà vers un tunnel. |
| `/zones/{zone}/dns_records/{record}` | DELETE | `cf route rm` | Sauté avec `--keep-dns`, et refusé si le contenu n'est pas un `.cfargotunnel.com`. |

## Les trois pièges que ce client encode

1. **`success: false` avec un HTTP 200.** L'API v4 répond régulièrement 200 en
   signalant l'échec dans l'enveloppe. Lire le code de statut, c'est rapporter
   un échec comme un résultat. Le client lit `success`.

2. **Le catch-all doit être le dernier.** La table d'ingress est ordonnée et lue
   de haut en bas. Un `http_status:404` placé ailleurs qu'à la fin avale toutes
   les règles suivantes — sans erreur : le tunnel démarre, le nom résout, et
   chaque requête répond 404. `Config.Normalise` le remet en dernier ;
   `Config.Validate` refuse une table qui n'en a pas.

3. **Un CNAME non proxifié ne mène nulle part.** `xxx.cfargotunnel.com` n'est
   joignable qu'à travers le réseau Cloudflare. Sans `proxied: true`, le nom
   résout vers une adresse qu'aucun client ne peut atteindre, et le symptôme est
   un délai d'attente, pas une erreur.
