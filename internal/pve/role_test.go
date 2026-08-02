package pve

import (
	"context"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

// L'écriture d'un rôle est le seul moyen d'accorder un privilège isolé : une
// ACL n'accorde qu'un rôle, et parmi les rôles intégrés du nœud, un privilège
// comme Sys.Modify n'est porté que par Administrator. Ces tests tiennent chaque
// méthode aux deux choses dont elle seule répond : la requête qu'elle émet, et
// ce qu'elle fait de la réponse.

func TestRoleWritesUseTheRightVerbsAndPaths(t *testing.T) {
	t.Run("create", func(t *testing.T) {
		c, s := newSpy(t, `{"data":null}`)
		if err := c.CreateRole(context.Background(), "ops-backup", []string{"Sys.Modify", "Sys.Audit"}); err != nil {
			t.Fatalf("CreateRole: %v", err)
		}
		if s.method != http.MethodPost || s.path != "/api2/json/access/roles" {
			t.Errorf("%s %s", s.method, s.path)
		}
		if s.body.Get("roleid") != "ops-backup" {
			t.Errorf("roleid = %q", s.body.Get("roleid"))
		}
		// Trié : le plan d'un --dry-run affiche cette chaîne, et deux exécutions
		// du même geste doivent en produire une seule.
		if got := s.body.Get("privs"); got != "Sys.Audit,Sys.Modify" {
			t.Errorf("privs = %q", got)
		}
	})

	t.Run("update", func(t *testing.T) {
		c, s := newSpy(t, `{"data":null}`)
		if err := c.UpdateRole(context.Background(), "ops-backup", []string{"Sys.Audit"}); err != nil {
			t.Fatalf("UpdateRole: %v", err)
		}
		if s.method != http.MethodPut || s.path != "/api2/json/access/roles/ops-backup" {
			t.Errorf("%s %s", s.method, s.path)
		}
		// LE test de cette famille. « append » ferait l'union CÔTÉ NŒUD, donc la
		// liste résultante ne serait connue qu'après l'écriture et un --dry-run
		// ne pourrait pas la montrer. pvecli fait l'union avant, et n'envoie
		// jamais ce paramètre.
		if _, present := s.body["append"]; present {
			t.Errorf("« append » ne doit JAMAIS partir : %v", s.body)
		}
		if got := s.body.Get("privs"); got != "Sys.Audit" {
			t.Errorf("privs = %q", got)
		}
	})

	t.Run("delete", func(t *testing.T) {
		c, s := newSpy(t, `{"data":null}`)
		if err := c.DeleteRole(context.Background(), "ops-backup"); err != nil {
			t.Fatalf("DeleteRole: %v", err)
		}
		if s.method != http.MethodDelete || s.path != "/api2/json/access/roles/ops-backup" {
			t.Errorf("%s %s", s.method, s.path)
		}
		// Un DELETE ne porte pas de formulaire : le serveur HTTP de PVE répond
		// 501 avant même la couche schéma (PVX-031).
		if len(s.body) != 0 {
			t.Errorf("corps sur un DELETE = %v", s.body)
		}
	})
}

// Le plan et la requête passent par le même rendu : ils ne peuvent donc pas
// diverger. La normalisation est ce qui rend ce rendu comparable.
func TestNormalizePrivilegesIsStableAndDeduplicated(t *testing.T) {
	got := NormalizePrivileges([]string{"Sys.Modify", " Sys.Audit ", "Sys.Modify", "", "VM.Backup,Sys.Audit"})
	want := []string{"Sys.Audit", "Sys.Modify", "VM.Backup"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("NormalizePrivileges = %v, want %v", got, want)
	}
	if got := NormalizePrivileges(nil); len(got) != 0 {
		t.Errorf("liste vide = %v", got)
	}
}

// Le prédicat transcrit la règle du nœud, il ne l'approxime pas.
//
// PVE::API2::Role::create_role refuse « /^PVE/i » — un préfixe INSENSIBLE À LA
// CASSE. Une vérification locale sensible à la casse laisserait passer
// « pveBackup » et « Pve-ops » jusqu'à l'écriture, pour les faire échouer côté
// nœud APRÈS la confirmation : le pire moment, puisque l'opérateur a déjà validé.
func TestIsBuiltinRoleNameCatchesThePVEFamily(t *testing.T) {
	for _, name := range []string{"PVEAuditor", "PVEVMAdmin", "Administrator", "NoAccess"} {
		if !IsBuiltinRoleName(name) {
			t.Errorf("%q doit être vu comme intégré", name)
		}
	}
	// La casse ne sauve pas : le nœud compare sans elle.
	for _, name := range []string{"pveBackup", "Pve-ops", "pvebackupjobadmin", "PVEBackupJobAdmin"} {
		if !IsBuiltinRoleName(name) {
			t.Errorf("%q tombe dans l'espace réservé /^PVE/i — le nœud le refuse", name)
		}
	}
	for _, name := range []string{"ops-backup", "URLFetch", "backup-ops", "administrator"} {
		if IsBuiltinRoleName(name) {
			t.Errorf("%q est un rôle sur mesure", name)
		}
	}
}

// Capture réelle du nœud : elle contient un rôle NON intégré (URLFetch,
// special=0) à côté des rôles livrés par PVE. Confondre les deux ferait refuser
// la modification d'un rôle sur mesure, ou accepter celle d'un rôle intégré.
func TestRolesDistinguishBuiltinsFromCustomOnes(t *testing.T) {
	c := replay(t, map[string]string{"GET /api2/json/access/roles": "roles-with-custom.json"})

	roles, err := c.Roles(context.Background())
	if err != nil {
		t.Fatalf("Roles: %v", err)
	}

	seen := map[string]Role{}
	for _, r := range roles {
		seen[r.RoleID] = r
	}
	custom, ok := seen["URLFetch"]
	if !ok {
		t.Fatalf("la capture doit contenir un rôle sur mesure : %+v", roles)
	}
	if custom.IsBuiltin() {
		t.Errorf("URLFetch porte special=0 — ce n'est pas un rôle intégré : %+v", custom)
	}
	if !seen["PVEAuditor"].IsBuiltin() {
		t.Errorf("PVEAuditor porte special=1 : %+v", seen["PVEAuditor"])
	}
	// Administrator est l'univers des privilèges de ce nœud : c'est lui que la
	// CLI relit pour valider une liste sans coder de liste en dur.
	if !strings.Contains(seen["Administrator"].Privs, "Sys.Modify") {
		t.Error("Administrator doit porter Sys.Modify — c'est le point de départ de PVX-077")
	}
}
