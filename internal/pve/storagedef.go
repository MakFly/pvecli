package pve

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// La DÉFINITION d'un stockage n'a rien à voir avec son CONTENU.
//
// internal/pve/storage.go parle à /nodes/{node}/storage… : ce qu'un nœud voit
// d'un stockage, son remplissage, ses volumes. Ici on parle à /storage, un
// endpoint de CLUSTER : la déclaration elle-même, écrite dans
// /etc/pve/storage.cfg et répliquée. Supprimer une définition ne supprime aucun
// octet ; supprimer un volume ne touche à aucune définition.
//
// Cette famille existe pour une raison précise : tant qu'aucun stockage ne
// déclare « backup » ailleurs que sur le disque du nœud, la seule destination
// possible est « local » — et une sauvegarde qui vit sur le disque de ce
// qu'elle protège meurt avec lui.
//
// Schéma vérifié contre l'API viewer PVE 9.x (search-pve-api.ts "/storage")
// et contre des captures réelles du nœud (testdata/storage-defs.json,
// testdata/storage-def.json).

// splitContentTypes découpe la déclaration « content », qui est une chaîne à
// virgules.
//
// L'ORDRE N'EST PAS STABLE : le même stockage local rend
// « backup,import,vztmpl,iso » par l'index et « import,backup,vztmpl,iso » par
// le détail, à une seconde d'intervalle. Comparer deux chaînes « content »
// octet à octet est donc faux — d'où SameContentTypes plus bas.
func splitContentTypes(content string) []string {
	if strings.TrimSpace(content) == "" {
		return nil
	}
	parts := strings.Split(content, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// contentAccepts dit si une déclaration « content » couvre un type donné.
func contentAccepts(content, want string) bool {
	for _, c := range splitContentTypes(content) {
		if strings.EqualFold(c, want) {
			return true
		}
	}
	return false
}

// SameContentTypes compare deux déclarations « content » comme des ENSEMBLES.
//
// C'est la seule comparaison honnête : PVE ne garantit pas l'ordre, et une
// commande qui conclurait « le contenu a changé » sur une permutation ferait
// écrire un PUT que personne n'a demandé.
func SameContentTypes(a, b string) bool {
	left, right := splitContentTypes(a), splitContentTypes(b)
	if len(left) != len(right) {
		return false
	}
	norm := func(in []string) []string {
		out := make([]string, len(in))
		for i, s := range in {
			out[i] = strings.ToLower(s)
		}
		sort.Strings(out)
		return out
	}
	left, right = norm(left), norm(right)
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

// StorageDef est une définition de stockage telle que GET /storage la rend.
//
// Les tags à TIRETS ne sont pas une coquetterie : « prune-backups » et
// « max-protected-backups » s'écrivent ainsi côté API, et un tag en camelCase
// décoderait silencieusement en zéro.
type StorageDef struct {
	Storage string `json:"storage"`
	Type    string `json:"type"`
	Content string `json:"content"`
	// Digest couvre TOUT /etc/pve/storage.cfg, pas cette seule entrée : les deux
	// stockages du lab portent le même. C'est la garde anti-écrasement
	// concurrent de PVE — un changement sur N'IMPORTE QUEL stockage entre la
	// lecture et l'écriture fait échouer le PUT.
	Digest string `json:"digest"`

	Server      string `json:"server"`
	Export      string `json:"export"`
	Share       string `json:"share"`
	Datastore   string `json:"datastore"`
	Path        string `json:"path"`
	Username    string `json:"username"`
	Domain      string `json:"domain"`
	Fingerprint string `json:"fingerprint"`
	Namespace   string `json:"namespace"`
	Nodes       string `json:"nodes"`
	Options     string `json:"options"`

	PruneBackups string `json:"prune-backups"`

	// Disable, Shared, Port et MaxProtectedBackups passent par flexInt : PVE les
	// rend en nombre depuis un endpoint et en chaîne depuis un autre.
	//
	// Disable n'a PAS besoin d'être un pointeur, contrairement à
	// BackupJob.Enabled : son absence vaut 0, c'est-à-dire « activé », qui est
	// bien le défaut. Confondre absent et 0 serait ici sans conséquence.
	Disable             flexInt `json:"disable"`
	Shared              flexInt `json:"shared"`
	Port                flexInt `json:"port"`
	MaxProtectedBackups flexInt `json:"max-protected-backups"`
}

// ContentTypes découpe la déclaration « content ».
func (s StorageDef) ContentTypes() []string { return splitContentTypes(s.Content) }

// Accepts dit si ce stockage prend le type de contenu demandé.
func (s StorageDef) Accepts(content string) bool { return contentAccepts(s.Content, content) }

// IsEnabled applique le défaut : « disable » absent veut dire activé.
func (s StorageDef) IsEnabled() bool { return s.Disable == 0 }

// Target rend la CIBLE réelle du stockage — l'endroit où les octets atterrissent.
// C'est l'information qui manque le plus dans une liste de noms : « pbs-infra »
// ne dit pas s'il pointe sur la baie du garage ou sur la même machine.
func (s StorageDef) Target() string {
	switch {
	case s.Server != "" && s.Export != "":
		return s.Server + ":" + s.Export
	case s.Server != "" && s.Share != "":
		return s.Server + ":" + s.Share
	case s.Server != "" && s.Datastore != "":
		return s.Server + ":" + s.Datastore
	case s.Server != "":
		return s.Server
	case s.Path != "":
		return s.Path
	default:
		return ""
	}
}

// nodeLocalStorageTypes nomme les types dont les octets vivent sur le disque du
// nœud lui-même.
//
// « dir » est le cas ambigu et il est classé ici volontairement : un répertoire
// PEUT être un point de montage NFS monté par l'OS, mais rien dans l'API ne
// permet de le savoir. Le classer comme distant ferait passer « local » pour
// une destination de sauvegarde valable — exactement le faux positif que cette
// famille existe pour éliminer.
var nodeLocalStorageTypes = map[string]bool{
	"dir": true, "lvm": true, "lvmthin": true, "zfspool": true, "btrfs": true,
}

// IsOffNodeBackupTarget dit si ce stockage peut recevoir une sauvegarde
// AILLEURS que sur le disque du nœud.
func (s StorageDef) IsOffNodeBackupTarget() bool {
	return s.IsEnabled() && s.Accepts("backup") && !nodeLocalStorageTypes[s.Type]
}

// ---------------------------------------------------------------- types

// storageTypeSpec décrit ce qu'un type de stockage exige et ce qu'il accepte.
//
// La table est délibérément courte : pvecli n'expose que les quatre types dont
// le schéma a été vérifié champ par champ. En accepter d'autres reviendrait à
// écrire des payloads de mémoire — ce que le PRD §6.3 interdit — et à laisser
// le nœud rendre un 400 que rien dans la sortie n'expliquerait.
type storageTypeSpec struct {
	// required nomme les drapeaux sans lesquels le stockage ne peut pas exister.
	required []string
	// contents nomme les types de contenu que PVE accepte pour ce type.
	contents []string
	// password dit ce que le type fait d'un mot de passe : "" aucun,
	// "optional" (le partage peut être monté en invité), "required".
	password string
	what     string
}

var storageTypeSpecs = map[string]storageTypeSpec{
	"nfs": {
		required: []string{"server", "export"},
		contents: []string{"images", "rootdir", "vztmpl", "iso", "backup", "snippets"},
		what:     "partage NFS",
	},
	"cifs": {
		required: []string{"server", "share"},
		contents: []string{"images", "rootdir", "vztmpl", "iso", "backup", "snippets"},
		password: "optional",
		what:     "partage SMB/CIFS",
	},
	"pbs": {
		required: []string{"server", "datastore", "username"},
		// Un PBS ne stocke QUE des sauvegardes. Y déclarer « iso » produit un
		// stockage que le nœud accepte et sur lequel rien n'atterrira jamais.
		contents: []string{"backup"},
		password: "required",
		what:     "Proxmox Backup Server",
	},
	"dir": {
		required: []string{"path"},
		contents: []string{"images", "rootdir", "vztmpl", "iso", "backup", "snippets", "import"},
		what:     "répertoire sur le nœud",
	},
}

// KnownStorageTypes nomme les types que pvecli sait déclarer, triés.
func KnownStorageTypes() []string {
	out := make([]string, 0, len(storageTypeSpecs))
	for name := range storageTypeSpecs {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// StorageTypeLabel rend le type en clair, pour un message d'erreur.
func StorageTypeLabel(typ string) string {
	if spec, ok := storageTypeSpecs[typ]; ok {
		return spec.what
	}
	return typ
}

// StorageTypeNeedsPassword dit ce que le type fait d'un mot de passe.
func StorageTypeNeedsPassword(typ string) (required, accepted bool) {
	spec, ok := storageTypeSpecs[typ]
	if !ok {
		return false, false
	}
	return spec.password == "required", spec.password != ""
}

// ValidateStorageDefOptions refuse LOCALEMENT une déclaration que le nœud
// rejetterait, ou pire, accepterait sans rien faire.
//
// Le second cas est la raison d'être de cette fonction : un PBS déclaré
// « content=iso » est accepté par PVE et ne recevra jamais rien. Aucun 400 ne
// prévient, et la panne ne se voit que le jour de la restauration.
func ValidateStorageDefOptions(o StorageDefOptions) error {
	spec, ok := storageTypeSpecs[o.Type]
	if !ok {
		return fmt.Errorf("type de stockage %q inconnu de pvecli — types vérifiés : %s.\n"+
			"Les autres types (lvm, zfspool, ceph…) se déclarent depuis l'interface web :\n"+
			"leur schéma n'a pas été vérifié ici, et l'écrire de mémoire est précisément\n"+
			"ce que ce projet refuse de faire",
			o.Type, strings.Join(KnownStorageTypes(), ", "))
	}

	for _, field := range spec.required {
		if o.value(field) == "" {
			return fmt.Errorf("un stockage de type %q (%s) exige --%s : sans lui, le nœud ne sait pas où écrire.\n"+
				"  champs exigés : --%s", o.Type, spec.what, field, strings.Join(spec.required, " --"))
		}
	}

	if o.Content == "" {
		return fmt.Errorf("--content est obligatoire.\n" +
			"L'API le donne pour optionnel et choisit un défaut à la place — qui peut très bien\n" +
			"ne pas contenir « backup ». On obtient alors un stockage d'apparence normale sur\n" +
			"lequel aucune sauvegarde n'atterrira jamais")
	}
	for _, c := range splitContentTypes(o.Content) {
		if !contentAccepts(strings.Join(spec.contents, ","), c) {
			return fmt.Errorf("un stockage de type %q (%s) n'accepte pas le contenu %q.\n"+
				"  contenus acceptés : %s", o.Type, spec.what, c, strings.Join(spec.contents, ", "))
		}
	}
	return nil
}

// ---------------------------------------------------------------- options

// StorageDefOptions est le payload d'une écriture de définition de stockage.
type StorageDefOptions struct {
	Storage string
	Type    string
	Content string

	Server      string
	Export      string
	Share       string
	Datastore   string
	Path        string
	Username    string
	Password    string
	Domain      string
	Fingerprint string
	Namespace   string
	Options     string
	Nodes       string
	// PruneBackups est UNE option string (« keep-last=3,keep-daily=7 »), donc un
	// REMPLACEMENT de la politique entière. Elle n'est pas éclatée en six
	// drapeaux --keep-* comme sur un job : ce serait promettre une unité de mise
	// à jour que l'API n'a pas.
	PruneBackups string
	Port         int
	Disable      bool
}

// value rend UN champ par le nom de son paramètre d'API — qui est aussi le nom
// du drapeau CLI. Cette table unique est ce qui empêche le plan affiché et la
// requête émise de diverger.
func (o StorageDefOptions) value(key string) string {
	switch key {
	case "type":
		return o.Type
	case "content":
		return o.Content
	case "server":
		return o.Server
	case "export":
		return o.Export
	case "share":
		return o.Share
	case "datastore":
		return o.Datastore
	case "path":
		return o.Path
	case "username":
		return o.Username
	case "password":
		return o.Password
	case "domain":
		return o.Domain
	case "fingerprint":
		return o.Fingerprint
	case "namespace":
		return o.Namespace
	case "options":
		return o.Options
	case "nodes":
		return o.Nodes
	case "prune-backups":
		return o.PruneBackups
	case "port":
		if o.Port > 0 {
			return strconv.Itoa(o.Port)
		}
		return ""
	case "disable":
		if o.Disable {
			return "1"
		}
		return "0"
	default:
		return ""
	}
}

// StorageDefPostOnlyKeys nomme les paramètres présents dans le schéma du POST et
// ABSENTS de celui du PUT.
//
// Conséquence directe, vérifiée : « storage def set » ne peut PAS repointer un
// NFS, un CIFS, un PBS ou un répertoire ailleurs. Il faut supprimer la
// définition et la recréer — ce qui n'efface aucune donnée, la suppression ne
// retirant que l'entrée de configuration.
var StorageDefPostOnlyKeys = []string{"datastore", "export", "path", "share", "type"}

// StorageDefUpdatableKeys nomme ce que le PUT accepte réellement.
var StorageDefUpdatableKeys = []string{
	"content", "disable", "domain", "fingerprint", "namespace", "nodes",
	"options", "password", "port", "prune-backups", "server", "username",
}

// Values rend le payload du POST.
func (o StorageDefOptions) Values() url.Values {
	v := url.Values{}
	v.Set("storage", o.Storage)
	v.Set("type", o.Type)
	for _, key := range []string{
		"content", "server", "export", "share", "datastore", "path",
		"username", "password", "domain", "fingerprint", "namespace",
		"options", "nodes", "prune-backups", "port",
	} {
		if value := o.value(key); value != "" {
			v.Set(key, value)
		}
	}
	// « disable » n'est envoyé que s'il est demandé : l'omettre laisse le défaut
	// du nœud, qui est « activé ».
	if o.Disable {
		v.Set("disable", "1")
	}
	return v
}

// UpdateValues rend le PUT PARTIEL : seules les clés nommées dans changed
// partent, plus le digest lu au pre-read.
//
// Le digest n'est pas une précaution de style. Il couvre tout
// /etc/pve/storage.cfg : si un autre stockage a bougé entre la lecture et
// l'écriture, le nœud refuse. L'échec est bruyant et la commande rejouable —
// c'est très exactement le comportement voulu face à deux administrateurs qui
// éditent en même temps.
func (o StorageDefOptions) UpdateValues(changed map[string]bool, digest string) url.Values {
	v := url.Values{}
	for _, key := range StorageDefUpdatableKeys {
		if !changed[key] {
			continue
		}
		v.Set(key, o.value(key))
	}
	if digest != "" {
		v.Set("digest", digest)
	}
	return v
}

// ---------------------------------------------------------------- client

// StorageDefs liste les définitions du cluster, triées par identifiant.
//
// La liste est FILTRÉE par les droits de l'appelant (Datastore.Audit ou
// Datastore.AllocateSpace sur /storage/<id>) : une liste vide veut dire
// « aucune définition VISIBLE », pas « aucune définition ».
//
// GET /storage
func (c *Client) StorageDefs(ctx context.Context) ([]StorageDef, error) {
	var out []StorageDef
	if err := c.get(ctx, epStorageDefs, nil, nil, &out); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Storage < out[j].Storage })
	return out, nil
}

// StorageDefByID lit une définition.
//
// ATTENTION : un identifiant inconnu répond HTTP 500, pas 404, avec
// « storage 'x' does not exist ». Le pre-read qui déduit « une erreur = le nom
// est libre » reste valable, mais l'erreur brute parlera d'erreur interne du
// nœud alors que le nom est simplement inconnu.
//
// GET /storage/{storage}
func (c *Client) StorageDefByID(ctx context.Context, id string) (*StorageDef, error) {
	var def StorageDef
	if err := c.get(ctx, epStorageDef, []string{id}, nil, &def); err != nil {
		return nil, err
	}
	// La réponse ne répète pas toujours l'identifiant : l'appelant l'a demandé.
	if def.Storage == "" {
		def.Storage = id
	}
	return &def, nil
}

// CreateStorageDef déclare un stockage.
//
// La réponse rend {storage, type, config} — un écho de la demande, pas une
// preuve. Elle est ignorée : ce qui prouve la création, c'est la relecture par
// StorageDefByID, faite par le post-read du pipeline de mutation.
//
// POST /storage
func (c *Client) CreateStorageDef(ctx context.Context, o StorageDefOptions) error {
	return c.post(ctx, epStorageDefNew, nil, o.Values(), nil)
}

// UpdateStorageDef modifie une définition. Seules les clés présentes dans v sont
// touchées — un PUT partiel, pas un remplacement.
//
// PUT /storage/{storage}
func (c *Client) UpdateStorageDef(ctx context.Context, id string, v url.Values) error {
	return c.post(ctx, epStorageDefSet, []string{id}, v, nil)
}

// DeleteStorageDef retire l'ENTRÉE DE CONFIGURATION du stockage.
//
// Les fichiers ne bougent pas : les archives sur le partage NFS/CIFS et les
// snapshots du datastore PBS restent où ils sont. C'est le miroir exact de
// DeleteBackupJob, qui supprime la planification et pas les archives.
//
// DELETE /storage/{storage}
func (c *Client) DeleteStorageDef(ctx context.Context, id string) error {
	return c.del(ctx, epStorageDefDel, []string{id}, nil, nil)
}

// StorageDefsPath et StorageDefPath rendent les chemins pour --dry-run.
func StorageDefsPath() string         { return epStorageDefs.Pattern }
func StorageDefPath(id string) string { return epStorageDef.Path(id) }
