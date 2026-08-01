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
| `/accounts` | GET | `pvecli cf status` | Sert à découvrir `cf.account_id` avant qu'il soit configuré — sinon l'opérateur devrait déjà connaître la valeur que la commande est censée lui donner. Un jeton qui ne voit aucun compte n'a pas de permission de niveau compte. |
| `/accounts/{account}/cfd_tunnel` | GET | `cf tunnel ls`, résolution par nom | Renvoie aussi les tunnels **supprimés** (`deleted_at` non nul) — le client les filtre. |
| `/accounts/{account}/cfd_tunnel` | POST | `cf tunnel create` | `config_src: "cloudflare"` = tunnel *remotely-managed* : la table d'ingress vit dans l'API, pas dans un `config.yml` de l'invité. |
| `/accounts/{account}/cfd_tunnel/{tunnel}` | DELETE | `cf tunnel rm` | Refusé tant qu'un connecteur est rattaché. Ce refus est une information. |
| `/accounts/{account}/cfd_tunnel/{tunnel}/token` | GET | `cf tunnel create` | Le jeton de connecteur. **Secret** : rangé au trousseau, jamais affiché ni passé en argument. |
| `/accounts/{account}/cfd_tunnel/{tunnel}/configurations` | GET | `cf route ls`, pré-lecture de `route add/rm` | La réponse enveloppe la table dans `{"config": {"ingress": [...]}}`. |
| `/accounts/{account}/cfd_tunnel/{tunnel}/configurations` | PUT | `cf route add`, `cf route rm` | **Remplace** la table entière. Le client la relit, la modifie, la renvoie — jamais un PUT partiel. |
| `/accounts/{account}/access/apps` | GET | `cf access app ls`, résolution par domaine | Une application est identifiée par le **nom public** qu'elle protège, pas par son nom d'affichage : deux applications peuvent porter le même nom, jamais le même domaine. |
| `/accounts/{account}/access/apps` | POST | `cf access app create` | `type: "self_hosted"` pour un service derrière un tunnel. L'application créée n'a **aucune policy** : elle refuse tout le monde, ce qui est l'état correct d'une porte qu'on vient de poser. |
| `/accounts/{account}/access/apps/{app}` | DELETE | `cf access app rm` | Retire la protection, **pas** la route du tunnel. Supprimer l'application sans supprimer la route rend le service public — d'où le refus renforcé. |
| `/accounts/{account}/access/apps/{app}/policies` | GET | `cf access policy ls`, pré-lecture de `route add` | Une application sans policy n'est pas une application ouverte : elle est fermée. La liste vide est donc une information, pas un vide. |
| `/accounts/{account}/access/apps/{app}/policies` | POST | `cf access policy add` | `decision` + `include`. Une personne : `{"email": {"email": …}}`. Un service token : `{"service_token": {"token_id": …}}`. |
| `/accounts/{account}/access/apps/{app}/policies/{policy}` | DELETE | `cf access policy rm` | Retirer la dernière policy referme l'application sur tout le monde — signalé, parce que ça ressemble à une ouverture. |
| `/accounts/{account}/access/service_tokens` | GET | `cf access token ls`, résolution par nom | Ne renvoie jamais `client_secret` : aucune relecture ne le rend. |
| `/accounts/{account}/access/service_tokens` | POST | `cf access token create` | `client_secret` n'est renvoyé qu'**ici**, une seule fois. **Secret** : rangé au trousseau, jamais affiché, retiré aussi de la sortie `-o json`. |
| `/zones` | GET | résolution de zone | Le suffixe le plus long gagne : avec `example.com` et `lab.example.com`, un nom en `.lab.example.com` appartient à la seconde. |
| `/zones/{zone}/dns_records` | GET | `cf route add/rm` | Filtré par `name` : sert à distinguer une création d'une mise à jour. |
| `/zones/{zone}/dns_records` | POST | `cf route add` | CNAME vers `{tunnel}.cfargotunnel.com`, **toujours** `proxied: true`. |
| `/zones/{zone}/dns_records/{record}` | PUT | `cf route add` | Seulement si l'enregistrement existant pointe déjà vers un tunnel. |
| `/zones/{zone}/dns_records/{record}` | DELETE | `cf route rm` | Sauté avec `--keep-dns`, et refusé si le contenu n'est pas un `.cfargotunnel.com`. |

## Les cinq pièges que ce client encode

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

4. **Le round-trip n'écrit que ce qu'il modélise.** Écrire une route relit la
   table, la modifie et la renvoie **entière**. Une clé absente des structs de
   `internal/cf/tunnel.go` traverse donc le décodage sans être retenue, et
   disparaît de *toutes* les règles au premier `route add` suivant. Conséquence
   pratique : une option posée à la main dans le dashboard ne survit pas. C'est
   pour ça que `originRequest.noTLSVerify` est un champ du client — et pas une
   case à cocher chez Cloudflare — et que
   `TestWritingOneRoutePreservesTheOptionsOfTheOthers` monte la garde.

5. **Un service token dans une policy « allow » n'authentifie rien.** L'API
   l'accepte sans broncher. Mais `allow` veut dire « une identité s'est
   authentifiée », et un service token ne porte aucune identité : la policy
   laisse alors passer **toutes** les requêtes, y compris celles qui ne
   présentent aucun token. La décision correcte est `non_identity` — « Service
   Auth » dans le dashboard. `Policy.Validate` refuse la combinaison, et
   `cf access policy add --service-token` pose `non_identity` tout seul.

   C'est le piège le plus cher du lot : il ne produit aucune erreur, et son
   symptôme est que tout fonctionne — pour n'importe qui.

## Le tunnel expose, Access protège

Ce sont deux objets indépendants, et rien côté Cloudflare n'oblige à poser le
second. `cf route add` interroge donc les applications Access après avoir écrit
la route, et le dit quand le nom qu'il vient de publier n'est couvert par
aucune. L'ordre sûr est l'inverse — la porte, puis la route :

```sh
pvecli cf access app create pve.exemple.tld --name "Proxmox lab"
pvecli cf access policy add --app pve.exemple.tld --name humains --email moi@…
pvecli cf access token create pvecli-collegue
pvecli cf access policy add --app pve.exemple.tld --name cli --service-token pvecli-collegue
pvecli cf route add pve.exemple.tld --tunnel lab-pve --service https://192.168.1.23:8006 --no-tls-verify
```

## `noTLSVerify`, et pourquoi l'interface Proxmox l'exige

Proxmox sert son API sous un certificat auto-signé. `cloudflared` valide le
certificat de l'origine comme n'importe quel client : sans
`originRequest.noTLSVerify`, le tunnel monte, le nom résout, l'authentification
Access passe — et chaque requête finit en **502**, à l'ultime saut, celui qu'on
regarde en dernier.

```sh
pvecli cf route add pve.exemple.tld --tunnel lab-pve \
    --service https://192.168.1.23:8006 --no-tls-verify
```

Le drapeau est refusé sur une origine `http://`, où il ne ferait rien tout en
laissant croire que la question du certificat est réglée. Le segment non vérifié
va du connecteur à l'hyperviseur, et ne quitte jamais le LAN : tout ce qui
traverse Internet reste du TLS Cloudflare vérifié.
