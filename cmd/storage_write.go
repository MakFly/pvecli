package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/MakFly/pvectl/internal/output"
	"github.com/MakFly/pvectl/internal/pve"
	"github.com/MakFly/pvectl/internal/service"
	"github.com/spf13/cobra"
)

// checkStorageAccepts refuses before the write when a storage cannot hold the
// content type asked for, and names the ones that can. PVE's own error states
// the content type and leaves the operator to guess where else to put it.
func checkStorageAccepts(ctx context.Context, client *pve.Client, node, storage, content string) error {
	stores, err := client.Storages(ctx, node)
	if err != nil {
		return err
	}

	var eligible []string
	found := false
	for _, s := range stores {
		if s.Accepts(content) {
			eligible = append(eligible, s.Storage)
		}
		if s.Storage == storage {
			found = true
			if s.Accepts(content) {
				return nil
			}
		}
	}

	if !found {
		return fmt.Errorf("le stockage %q n'existe pas sur %s — vois « pvectl storage ls »", storage, node)
	}
	if len(eligible) == 0 {
		return fmt.Errorf("aucun stockage de %s n'accepte le type « %s »", node, content)
	}
	return fmt.Errorf(
		"le stockage %q n'accepte pas le type « %s ».\n"+
			"C'est une contrainte de l'API, pas une convention de nommage.\n"+
			"Ceux qui l'acceptent :\n  %s",
		storage, content, strings.Join(eligible, "\n  "))
}

// warnMissingChecksum says out loud what an unverified image costs. It is a
// warning and not a refusal: the operator may legitimately be fetching
// something they will verify otherwise.
func warnMissingChecksum(cmd *cobra.Command, checksum, content string) {
	if checksum != "" {
		return
	}
	what := "cette image"
	if content == "vztmpl" {
		what = "ce template"
	}
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
		"AVERTISSEMENT : aucune somme de contrôle fournie.\n"+
			"  %s deviendra la source de tes VM ; personne ne vérifiera jamais\n"+
			"  qu'elle est bien celle que l'éditeur a publiée. Une image altérée en\n"+
			"  transit se propage ensuite à chaque clone, sans rien signaler.\n"+
			"  --checksum <sha256> --checksum-algorithm sha256\n", what)
}

// ---------------------------------------------------------------- download-url

func newStorageDownloadCmd() *cobra.Command {
	var o pve.DownloadOptions

	c := &cobra.Command{
		Use:   "download-url <storage>",
		Short: "Fait télécharger un fichier PAR LE NŒUD (POST .../download-url)",
		Long: `Dépose une ISO, un template LXC ou une image de VM sur un stockage.

C'EST LE NŒUD QUI TÉLÉCHARGE. Le fichier ne transite pas par ta machine : PVE
ouvre lui-même la connexion vers l'URL. Une image de 4 Go passe donc par le lien
du nœud, pas par le tien — et la progression à regarder est le journal de la
tâche, pas un compteur local.

Le type de contenu est contraint à iso, vztmpl ou import : un disque de VM ne
se dépose pas par ici.

LA SOMME DE CONTRÔLE N'EST PAS UNE FORMALITÉ. L'image déposée aujourd'hui
deviendra le template cloné demain, puis chaque VM issue de ce template. Une
altération en transit se propage à toute la chaîne sans jamais rien signaler.
C'est le seul moment où la vérification coûte une ligne.

Endpoint : POST /api2/json/nodes/{node}/storage/{storage}/download-url`,
		Args: usage(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			storage := args[0]
			if o.URL == "" {
				return &exitError{code: pve.ExitUsage, msg: "--url est obligatoire"}
			}
			if o.Filename == "" {
				o.Filename = filenameFromURL(o.URL)
			}
			if (o.Checksum == "") != (o.Algorithm == "") {
				return &exitError{
					code: pve.ExitUsage,
					msg:  "--checksum et --checksum-algorithm vont ensemble : le nœud refuse l'un sans l'autre",
				}
			}

			opts, err := renderOptions(cmd)
			if err != nil {
				return err
			}
			client, err := newClient(cmd)
			if err != nil {
				return err
			}
			node, err := targetNode(cmd, nil)
			if err != nil {
				return err
			}

			warnMissingChecksum(cmd, o.Checksum, o.Content)

			return runVolumeArrival(cmd, client, opts, node, storage, o.Content, o.Filename, service.Plan{
				Node:     node,
				Method:   "POST",
				Path:     pve.DownloadPath(node, storage),
				Payload:  o.Values(),
				Effect:   fmt.Sprintf("le nœud %s télécharge %s vers %s", node, o.URL, storage),
				Rollback: fmt.Sprintf("pvectl storage rm %s %s:%s/%s", storage, storage, o.Content, o.Filename),
				Verify:   "le volume doit apparaître dans le contenu du stockage",
			}, func(ctx context.Context) (string, error) {
				return client.DownloadURL(ctx, node, storage, o)
			})
		},
	}

	f := c.Flags()
	f.StringVar(&o.URL, "url", "", "URL http(s) que le NŒUD ira chercher")
	f.StringVar(&o.Content, "content", "iso", "type de contenu : iso, vztmpl, import")
	f.StringVar(&o.Filename, "filename", "", "nom du fichier créé (défaut : dernier segment de l'URL)")
	f.StringVar(&o.Checksum, "checksum", "", "somme de contrôle attendue — fortement recommandée")
	f.StringVar(&o.Algorithm, "checksum-algorithm", "", "md5, sha1, sha224, sha256, sha384, sha512")
	f.StringVar(&o.Compression, "compression", "", "décompresse à l'arrivée : gz, xz, zst…")
	f.BoolVar(&o.SkipTLSVerify, "no-verify-certificates", false,
		"le nœud ne vérifie pas le certificat de l'URL — il fait alors confiance à n'importe qui")
	addWriteFlags(c)
	addRenderFlags(c)
	return c
}

// filenameFromURL keeps the last path segment, which is what an operator means
// by « the file » nine times out of ten. PVE normalises it anyway.
func filenameFromURL(raw string) string {
	trimmed := raw
	if i := strings.IndexAny(trimmed, "?#"); i >= 0 {
		trimmed = trimmed[:i]
	}
	return filepath.Base(trimmed)
}

// ---------------------------------------------------------------- upload

func newStorageUploadCmd() *cobra.Command {
	var o pve.UploadOptions

	c := &cobra.Command{
		Use:   "upload <storage> <fichier>",
		Short: "Pousse un fichier local vers un stockage (POST .../upload)",
		Long: `Envoie un fichier de TA machine vers un stockage du nœud.

La différence avec « download-url » est le chemin des octets :

  download-url    nœud → Internet            ta machine ne voit rien passer
  upload          ta machine → pveproxy → /var/tmp/pveupload-… → stockage

Quand le fichier est joignable par une URL, « download-url » est presque
toujours le bon choix : plus rapide, et il n'occupe pas ton lien montant.

Cette requête est la seule de la CLI qui ne soit pas form-encodée. Le corps est
un multipart dont le nœud attend les parties DANS UN ORDRE PRÉCIS — content,
puis checksum-algorithm, puis checksum, puis le fichier en dernier. Son parseur
est une machine à états ancrée en début de tampon, pas un parseur général.

Endpoint : POST /api2/json/nodes/{node}/storage/{storage}/upload`,
		Args: usage(cobra.ExactArgs(2)),
		RunE: func(cmd *cobra.Command, args []string) error {
			storage, path := args[0], args[1]

			info, err := os.Stat(path)
			if err != nil {
				return fmt.Errorf("fichier illisible : %w", err)
			}
			if info.IsDir() {
				return &exitError{code: pve.ExitUsage, msg: path + " est un répertoire"}
			}
			if o.Filename == "" {
				o.Filename = filepath.Base(path)
			}
			if (o.Checksum == "") != (o.Algorithm == "") {
				return &exitError{
					code: pve.ExitUsage,
					msg:  "--checksum et --checksum-algorithm vont ensemble : le nœud refuse l'un sans l'autre",
				}
			}

			opts, err := renderOptions(cmd)
			if err != nil {
				return err
			}
			client, err := newClient(cmd)
			if err != nil {
				return err
			}
			node, err := targetNode(cmd, nil)
			if err != nil {
				return err
			}

			warnMissingChecksum(cmd, o.Checksum, o.Content)

			plan := service.Plan{
				Node:     node,
				Method:   "POST",
				Path:     pve.UploadPath(node, storage),
				Payload:  o.Values(),
				Effect:   fmt.Sprintf("envoie %s (%s) vers %s", path, output.Bytes(info.Size()), storage),
				Rollback: fmt.Sprintf("pvectl storage rm %s %s:%s/%s", storage, storage, o.Content, o.Filename),
				Verify:   "le volume doit apparaître dans le contenu du stockage",
			}

			return runVolumeArrival(cmd, client, opts, node, storage, o.Content, o.Filename, plan,
				func(ctx context.Context) (string, error) {
					f, err := os.Open(path)
					if err != nil {
						return "", err
					}
					defer func() { _ = f.Close() }()

					reader := newProgressReader(f, info.Size(), cmd.ErrOrStderr(),
						output.IsTerminal(cmd.ErrOrStderr()))
					defer reader.done()

					return client.UploadFile(ctx, node, storage, o, info.Size(), reader)
				})
		},
	}

	f := c.Flags()
	f.StringVar(&o.Content, "content", "iso", "type de contenu : iso, vztmpl, import")
	f.StringVar(&o.Filename, "filename", "", "nom du volume créé (défaut : nom du fichier local)")
	f.StringVar(&o.Checksum, "checksum", "", "somme de contrôle attendue")
	f.StringVar(&o.Algorithm, "checksum-algorithm", "", "md5, sha1, sha224, sha256, sha384, sha512")
	addWriteFlags(c)
	addRenderFlags(c)
	return c
}

// runVolumeArrival drives download-url and upload through the same pipeline:
// same pre-read (does this storage take this content type?), same proof (is
// the volume actually there afterwards?).
func runVolumeArrival(
	cmd *cobra.Command,
	client *pve.Client,
	opts output.Options,
	node, storage, content, filename string,
	plan service.Plan,
	write func(context.Context) (string, error),
) error {
	runner := newRunner(cmd, client)

	result, err := runner.Run(cmd.Context(), service.Mutation{
		Target: storage,
		Plan:   plan,

		PreRead: func(ctx context.Context) (service.State, error) {
			if err := checkStorageAccepts(ctx, client, node, storage, content); err != nil {
				return service.State{}, err
			}
			return service.State{Exists: true, Status: "stockage prêt"}, nil
		},
		Write: write,

		// The proof is the volume listed by the node, not the exitstatus of
		// the task. A download that reports OK and leaves nothing behind is
		// exactly the failure this project exists to catch.
		PostRead: func(ctx context.Context) (service.State, error) {
			vols, err := client.StorageContent(ctx, node, storage, content)
			if err != nil {
				return service.State{}, err
			}
			for _, v := range vols {
				if strings.HasSuffix(v.VolID, "/"+filename) {
					return service.State{
						Exists: true, Status: "déposé",
						Summary: fmt.Sprintf("%s — %s", v.VolID, output.Bytes(v.Size)),
						Raw:     v,
					}, nil
				}
			}
			return service.State{}, fmt.Errorf(
				"la tâche s'est terminée mais %q n'est pas dans le contenu de %s", filename, storage)
		},
	})
	if err != nil {
		return err
	}

	vol, ok := result.Raw.(pve.Volume)
	if !ok {
		return nil
	}
	rows := output.Rows{Headers: []string{"CHAMP", "VALEUR"}, Cells: [][]string{
		{"volid", vol.VolID},
		{"content", vol.Content},
		{"taille", output.Bytes(vol.Size)},
	}}
	return output.Render(cmd.OutOrStdout(), opts, vol, rows)
}

// ---------------------------------------------------------------- rm

func newStorageRemoveCmd() *cobra.Command {
	c := &cobra.Command{
		Use:     "rm <storage> <volid>",
		Aliases: []string{"delete"},
		Short:   "Supprime un volume (DELETE .../content/{volume})",
		Long: `Supprime un volume d'un stockage : ISO, template, archive, disque.

Le second argument est un VOLID — « local:iso/debian.iso » — et pas un chemin
de système de fichiers. C'est la forme que tous les autres endpoints attendent,
et « pvectl storage content » est là pour te la donner.

Il n'y a pas de corbeille. Un disque de VM supprimé ici est perdu, et une
archive supprimée ici est la sauvegarde que tu n'auras pas au moment où tu en
auras besoin. La confirmation exige de retaper le volid.

Endpoint : DELETE /api2/json/nodes/{node}/storage/{storage}/content/{volume}`,
		Args: usage(cobra.ExactArgs(2)),
		RunE: func(cmd *cobra.Command, args []string) error {
			storage, volid := args[0], args[1]

			client, err := newClient(cmd)
			if err != nil {
				return err
			}
			node, err := targetNode(cmd, nil)
			if err != nil {
				return err
			}

			runner := newRunner(cmd, client)
			_, err = runner.Run(cmd.Context(), service.Mutation{
				Target:      volid,
				Destructive: true,
				Plan: service.Plan{
					Node:     node,
					Method:   "DELETE",
					Path:     pve.VolumePath(node, storage, volid),
					Effect:   fmt.Sprintf("supprime définitivement %s de %s", volid, storage),
					Rollback: "aucun — il n'y a pas de corbeille sur un stockage PVE",
					Verify:   "le volume ne doit plus apparaître dans le contenu du stockage",
				},

				PreRead: func(ctx context.Context) (service.State, error) {
					vols, err := client.StorageContent(ctx, node, storage, "")
					if err != nil {
						return service.State{}, err
					}
					for _, v := range vols {
						if v.VolID == volid {
							_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
								"volume visé : %s — %s, type %s\n",
								v.VolID, output.Bytes(v.Size), v.Content)
							return service.State{
								Exists: true, Status: "présent",
								Summary: v.VolID, Raw: v,
							}, nil
						}
					}
					return service.State{}, fmt.Errorf(
						"aucun volume %q sur %s — un volid s'écrit « %s:iso/fichier.iso ».\n"+
							"  pvectl storage content %s", volid, storage, storage, storage)
				},
				Write: func(ctx context.Context) (string, error) {
					return client.DeleteVolume(ctx, node, storage, volid)
				},
				PostRead: func(ctx context.Context) (service.State, error) {
					vols, err := client.StorageContent(ctx, node, storage, "")
					if err != nil {
						return service.State{}, err
					}
					for _, v := range vols {
						if v.VolID == volid {
							return service.State{}, fmt.Errorf("%s est toujours sur %s", volid, storage)
						}
					}
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "%s supprimé de %s\n", volid, storage)
					return service.State{Exists: true, Status: "supprimé"}, nil
				},
			})
			return err
		},
	}
	addWriteFlags(c)
	return c
}

// ---------------------------------------------------------------- progress

// progressReader reports how much of a local file has gone up the wire.
//
// It counts what was READ from the file, which is what the multipart writer
// has handed to the socket — not what the node has received. The distinction
// matters at the end: the counter hits 100 % while the node is still writing
// its temporary file, so the last step of an upload always looks like a pause.
type progressReader struct {
	src   io.Reader
	total int64
	read  atomic.Int64

	out   io.Writer
	live  bool
	last  time.Time
	shown bool
}

func newProgressReader(src io.Reader, total int64, out io.Writer, live bool) *progressReader {
	return &progressReader{src: src, total: total, out: out, live: live}
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.src.Read(b)
	if n > 0 {
		p.read.Add(int64(n))
		p.tick()
	}
	return n, err
}

func (p *progressReader) tick() {
	// Throttled: a redraw per read call would spend more time on the terminal
	// than on the transfer. In a CI log, where \r means nothing, stay silent
	// until the end.
	if !p.live || time.Since(p.last) < 200*time.Millisecond {
		return
	}
	p.last = time.Now()
	p.shown = true
	sent := p.read.Load()
	_, _ = fmt.Fprintf(p.out, "\r  envoi %s / %s (%d %%)   ",
		output.Bytes(sent), output.Bytes(p.total), 100*sent/max64(p.total, 1))
}

func (p *progressReader) done() {
	if p.shown {
		_, _ = fmt.Fprintf(p.out, "\r  envoi %s — terminé, le nœud vérifie le fichier\n",
			output.Bytes(p.read.Load()))
	}
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
