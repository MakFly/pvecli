package pve

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Un job de sauvegarde PLANIFIÉ n'a rien à voir avec `backup run`.
//
// `backup run` (POST /nodes/{node}/vzdump) lance une sauvegarde maintenant : la
// preuve qu'elle a eu lieu, c'est qu'on était là pour la lancer. Un job vit dans
// /etc/pve/jobs.cfg, il est répliqué à tout le cluster, et il tourne quand
// personne ne regarde — c'est justement ce qui en fait la seule sauvegarde qui
// existera le jour de la panne, et la seule dont la panne SILENCIEUSE ne se voit
// pas. D'où le fait que cette famille lise « next-run » et l'affiche : un job
// désactivé et un job qui échoue ont exactement la même tête dans une liste qui
// ne montrerait que des noms.
//
// Schéma vérifié contre l'API viewer PVE 9.x (search-pve-api.ts "/cluster/backup").

// BackupRetention est la politique de rétention d'un job.
//
// PVE la transporte dans « prune-backups », une option string du genre
// « keep-last=3,keep-daily=7 ». Elle REMPLACE celle du stockage, elle ne s'y
// ajoute pas : un job qui déclare keep-last=3 garde trois archives même si le
// stockage en autorisait cinquante.
//
// Le zéro n'est pas neutre — un champ à 0 n'est simplement pas envoyé, sinon
// « keep-daily=0 » demanderait à PVE de n'en garder aucune.
type BackupRetention struct {
	Last    int
	Hourly  int
	Daily   int
	Weekly  int
	Monthly int
	Yearly  int
}

// Empty dit qu'aucune rétention n'a été demandée. C'est le cas qui compte : sans
// rétention, un job planifié écrit indéfiniment et finit par remplir le stockage
// — la panne de disque que la sauvegarde devait éviter, causée par la sauvegarde.
func (r BackupRetention) Empty() bool {
	return r.Last == 0 && r.Hourly == 0 && r.Daily == 0 &&
		r.Weekly == 0 && r.Monthly == 0 && r.Yearly == 0
}

// String rend la valeur « prune-backups ». L'ordre est fixe pour que la sortie
// d'un --dry-run soit comparable d'une exécution à l'autre.
func (r BackupRetention) String() string {
	pairs := []struct {
		key string
		n   int
	}{
		{"keep-last", r.Last},
		{"keep-hourly", r.Hourly},
		{"keep-daily", r.Daily},
		{"keep-weekly", r.Weekly},
		{"keep-monthly", r.Monthly},
		{"keep-yearly", r.Yearly},
	}
	out := make([]string, 0, len(pairs))
	for _, p := range pairs {
		if p.n > 0 {
			out = append(out, p.key+"="+strconv.Itoa(p.n))
		}
	}
	return strings.Join(out, ",")
}

// RetentionKeys nomme les six compteurs, dans l'ordre de rendu.
var RetentionKeys = []string{
	"keep-last", "keep-hourly", "keep-daily", "keep-weekly", "keep-monthly", "keep-yearly",
}

// Set écrit UN compteur, par son nom d'option.
//
// C'est ce qui permet une modification par fusion plutôt que par remplacement :
// « prune-backups » est une valeur ENTIÈRE côté API, donc envoyer la seule
// option qu'on vient de changer efface les cinq autres — et supprime des
// archives que personne n'avait demandé de supprimer. L'appelant part de la
// rétention lue sur le nœud et n'écrase que ce qu'il a explicitement reçu.
func (r *BackupRetention) Set(key string, n int) bool {
	switch key {
	case "keep-last":
		r.Last = n
	case "keep-hourly":
		r.Hourly = n
	case "keep-daily":
		r.Daily = n
	case "keep-weekly":
		r.Weekly = n
	case "keep-monthly":
		r.Monthly = n
	case "keep-yearly":
		r.Yearly = n
	default:
		return false
	}
	return true
}

// ParseBackupRetention relit un « prune-backups » rendu par le nœud. Les clés
// inconnues (keep-all notamment) sont ignorées : elles ne sont pas des compteurs.
func ParseBackupRetention(s string) BackupRetention {
	var r BackupRetention
	opts := ParseOptionString(s)
	fields := map[string]*int{
		"keep-last": &r.Last, "keep-hourly": &r.Hourly, "keep-daily": &r.Daily,
		"keep-weekly": &r.Weekly, "keep-monthly": &r.Monthly, "keep-yearly": &r.Yearly,
	}
	for key, target := range fields {
		if n, err := strconv.Atoi(opts.Get(key)); err == nil {
			*target = n
		}
	}
	return r
}

// BackupJob est un job tel que GET /cluster/backup le rend.
type BackupJob struct {
	ID       string `json:"id"`
	Schedule string `json:"schedule"`
	Storage  string `json:"storage"`
	// VMID est une LISTE en CSV, pas un entier : « 220,221 ».
	VMID string  `json:"vmid"`
	Pool string  `json:"pool"`
	All  flexInt `json:"all"`
	// Enabled est un pointeur parce que son absence ne veut PAS dire 0 : le
	// schéma déclare enabled=1 par défaut. Confondre « absent » et « désactivé »
	// ferait afficher « inactif » sur un job qui tourne.
	Enabled          *flexInt `json:"enabled"`
	Mode             string   `json:"mode"`
	Compress         string   `json:"compress"`
	Comment          string   `json:"comment"`
	Node             string   `json:"node"`
	PruneBackups     string   `json:"prune-backups"`
	MailNotification string   `json:"mailnotification"`
	Mailto           string   `json:"mailto"`
	NotesTemplate    string   `json:"notes-template"`
	// NextRun est un epoch en secondes. C'est la seule colonne qui prouve que le
	// planificateur a bien pris le job : une planification que PVE n'a pas su
	// lire n'a pas de prochaine exécution.
	NextRun flexInt `json:"next-run"`
	Type    string  `json:"type"`
	// Remove est l'INTERRUPTEUR de la rétention, et il est rendu par le GET.
	// « prune-backups » sans « remove » ne purge rien : afficher la politique
	// sans lire cet interrupteur ferait croire le stockage protégé alors que
	// rien ne le purge — précisément la panne silencieuse que cette famille
	// existe pour rendre visible. Pointeur pour la même raison qu'Enabled : le
	// schéma le déclare à 1 par défaut, absent ne veut pas dire 0.
	Remove *flexInt `json:"remove"`
}

// IsEnabled applique le défaut du schéma (1) quand le champ est absent.
func (j BackupJob) IsEnabled() bool { return j.Enabled == nil || *j.Enabled != 0 }

// Prunes dit si la rétention du job est effectivement appliquée.
func (j BackupJob) Prunes() bool { return j.Remove == nil || *j.Remove != 0 }

// RetentionSummary rend la rétention telle qu'elle AGIT, pas telle qu'elle est
// écrite. Une politique déclarée mais inerte est le pire des deux mondes : elle
// rassure sans rien faire.
func (j BackupJob) RetentionSummary() string {
	policy := j.Retention().String()
	switch {
	case policy == "":
		return ""
	case !j.Prunes():
		return policy + " (INERTE : remove=0)"
	default:
		return policy
	}
}

// Target rend la cible du job en une chaîne lisible. Les trois formes sont
// exclusives côté API, et « all » l'emporte sur les deux autres.
func (j BackupJob) Target() string {
	switch {
	case j.All != 0:
		return "tous les guests"
	case j.Pool != "":
		return "pool " + j.Pool
	case j.VMID != "":
		return j.VMID
	default:
		return "—"
	}
}

// Retention relit la rétention effective du job.
func (j BackupJob) Retention() BackupRetention { return ParseBackupRetention(j.PruneBackups) }

// BackupJobOptions sont les paramètres de création d'un job planifié.
type BackupJobOptions struct {
	// ID est facultatif : PVE en génère un si on n'en impose pas. En imposer un
	// rend la commande rejouable et le job identifiable ailleurs que dans l'UI.
	ID       string
	Schedule string
	Storage  string
	VMIDs    []int
	Pool     string
	All      bool
	Mode     BackupMode
	Compress string
	Comment  string
	// Node restreint l'exécution à un nœud. Vide = n'importe lequel du cluster.
	Node             string
	Notes            string
	MailNotification string
	Mailto           string
	Enabled          bool
	Retention        BackupRetention
}

// Values rend le payload envoyé.
func (o BackupJobOptions) Values() url.Values {
	v := url.Values{}
	if o.ID != "" {
		v.Set("id", o.ID)
	}
	if o.Schedule != "" {
		v.Set("schedule", o.Schedule)
	}
	if o.Storage != "" {
		v.Set("storage", o.Storage)
	}

	// « all » est TOUJOURS énoncé, comme « enabled » plus bas, et pour la même
	// raison : c'est un booléen dont l'absence vaut « faux » côté schéma mais
	// « ne change rien » côté PUT. L'omettre quand il est faux laissait
	// `changedBackupJobValues` envoyer une chaîne vide sur un champ booléen.
	switch {
	case o.All:
		v.Set("all", "1")
	case o.Pool != "":
		v.Set("all", "0")
		v.Set("pool", o.Pool)
	default:
		v.Set("all", "0")
		ids := make([]string, 0, len(o.VMIDs))
		for _, id := range o.VMIDs {
			ids = append(ids, strconv.Itoa(id))
		}
		if len(ids) > 0 {
			v.Set("vmid", strings.Join(ids, ","))
		}
	}

	if o.Mode != "" {
		v.Set("mode", string(o.Mode))
	}
	if o.Compress != "" {
		v.Set("compress", o.Compress)
	}
	if o.Comment != "" {
		v.Set("comment", o.Comment)
	}
	if o.Node != "" {
		v.Set("node", o.Node)
	}
	if o.Notes != "" {
		v.Set("notes-template", o.Notes)
	}
	if o.MailNotification != "" {
		v.Set("mailnotification", o.MailNotification)
	}
	if o.Mailto != "" {
		v.Set("mailto", o.Mailto)
	}
	if o.Enabled {
		v.Set("enabled", "1")
	} else {
		v.Set("enabled", "0")
	}

	// « remove » vaut 1 par défaut dans le schéma et déclenche la purge selon
	// « prune-backups ». Les deux sont donc liés, et pvecli les envoie ensemble
	// ou pas du tout : un remove=1 sans rétention explicite fait appliquer une
	// politique qu'on n'a pas écrite, un prune-backups sans remove n'a aucun
	// effet. Les dissocier, c'est fabriquer les deux surprises.
	if !o.Retention.Empty() {
		v.Set("prune-backups", o.Retention.String())
		v.Set("remove", "1")
	} else {
		v.Set("remove", "0")
	}
	return v
}

// BackupJobs liste les jobs planifiés, triés par identifiant.
//
// GET /cluster/backup
func (c *Client) BackupJobs(ctx context.Context) ([]BackupJob, error) {
	var jobs []BackupJob
	if err := c.get(ctx, epBackupJobs, nil, nil, &jobs); err != nil {
		return nil, err
	}
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].ID < jobs[j].ID })
	return jobs, nil
}

// BackupJobByID lit un job.
//
// GET /cluster/backup/{id}
func (c *Client) BackupJobByID(ctx context.Context, id string) (*BackupJob, error) {
	var job BackupJob
	if err := c.get(ctx, epBackupJob, []string{id}, nil, &job); err != nil {
		return nil, err
	}
	// La réponse ne répète pas toujours l'identifiant : l'appelant l'a demandé.
	if job.ID == "" {
		job.ID = id
	}
	return &job, nil
}

// CreateBackupJob crée un job planifié.
//
// La réponse ne rend pas l'identifiant généré : quand on n'en impose pas, le
// seul moyen honnête de savoir lequel vient d'être créé est de relire la liste.
//
// POST /cluster/backup
func (c *Client) CreateBackupJob(ctx context.Context, o BackupJobOptions) error {
	return c.post(ctx, epBackupJobNew, nil, o.Values(), nil)
}

// UpdateBackupJob modifie un job existant. Seules les clés présentes dans v sont
// touchées — un PUT partiel, pas un remplacement.
//
// PUT /cluster/backup/{id}
func (c *Client) UpdateBackupJob(ctx context.Context, id string, v url.Values) error {
	return c.post(ctx, epBackupJobSet, []string{id}, v, nil)
}

// DeleteBackupJob supprime la définition du job. Les archives déjà écrites ne
// sont pas touchées : c'est la PLANIFICATION qui disparaît, donc les sauvegardes
// à venir, pas celles du passé.
//
// DELETE /cluster/backup/{id}
func (c *Client) DeleteBackupJob(ctx context.Context, id string) error {
	return c.del(ctx, epBackupJobDel, []string{id}, nil, nil)
}

// BackupJobsPath et BackupJobPath rendent les chemins pour --dry-run.
func BackupJobsPath() string         { return epBackupJobs.Pattern }
func BackupJobPath(id string) string { return epBackupJob.Path(id) }

// FormatNextRun rend la prochaine exécution. Un job sans prochaine exécution est
// une information, pas un vide : soit il est désactivé, soit sa planification
// n'a pas été comprise par le nœud.
func FormatNextRun(epoch int64, enabled bool) string {
	if epoch <= 0 {
		if !enabled {
			return "désactivé"
		}
		return "jamais — planification non retenue"
	}
	// Même format que output.Timestamp — la mise en forme n'est pas dupliquée
	// par goût, elle l'est parce que internal/pve ne dépend d'aucune couche de
	// présentation, et ne doit pas commencer ici.
	//
	// Le fuseau est celui du POSTE, pas celui du nœud : PVE ne rend qu'un
	// epoch, il n'annonce pas sa zone. Sur une colonne dont tout l'intérêt est
	// d'être crue, l'écart se voit — d'où le suffixe.
	t := time.Unix(epoch, 0)
	zone, _ := t.Zone()
	return t.Format("2006-01-02 15:04") + " " + zone
}

// ValidateJobID refuse un identifiant que le nœud rejettera, avant l'appel.
//
// Le contrôle est délibérément MINIMAL : « pve-configid » est plus strict que
// ça, mais les identifiants générés par PVE ne le respectent pas toujours
// eux-mêmes, et refuser ici un id que le nœud accepte serait pire que le 400
// qu'on cherche à éviter. On ne rejette donc que ce qui ne peut jamais passer :
// le vide, l'espace, et le '/' qui inventerait un segment d'URL.
func ValidateJobID(id string) error {
	if id == "" {
		return fmt.Errorf("identifiant vide")
	}
	if strings.ContainsAny(id, " \t\n/") {
		return fmt.Errorf("l'identifiant %q contient un espace ou un '/' — le nœud le refusera "+
			"(format « pve-configid » : lettres, chiffres, '-' et '_')", id)
	}
	return nil
}

// ValidateSchedule refuse une planification vide avant l'appel. PVE accepterait
// un job sans « schedule » : il ne tournerait jamais, sans rien dire.
func ValidateSchedule(s string) error {
	if strings.TrimSpace(s) == "" {
		return fmt.Errorf("une planification vide crée un job qui ne s'exécute JAMAIS — précise --schedule")
	}
	return nil
}
