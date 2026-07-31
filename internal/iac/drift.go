package iac

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/MakFly/pvecli/internal/pve"
)

// Live is a guest as the node reports it, reduced to the attributes Terraform
// declares. Everything else about the guest is deliberately absent: comparing
// what nobody declared is how a drift report fills with noise.
type Live struct {
	VMID   int
	Name   string
	Node   string
	Cores  int
	Memory int
	Tags   []string
	OnBoot *bool

	Disks    map[string]LiveDisk // keyed by interface: scsi0, virtio0, rootfs…
	Networks []LiveNIC           // ordered: net0, net1, …
}

// LiveDisk is one disk of the running configuration.
type LiveDisk struct {
	Datastore string
	SizeGiB   int
}

// LiveNIC is one interface of the running configuration.
type LiveNIC struct {
	Bridge string
	Model  string
	VLAN   int
}

// Ignored lists the attributes drift comparison deliberately does NOT look at,
// and why. It is exported because the criterion of PVX-044 is not "exclude the
// noisy fields" but "say which fields are excluded" — an exclusion an operator
// cannot see is a blind spot they will trip over exactly once.
var Ignored = []struct{ Field, Why string }{
	{"adresse IP", "en DHCP elle change sans que rien n'ait dérivé ; Terraform ne la déclare pas, il la lit"},
	{"statut d'exécution", "démarré ou arrêté n'est pas déclaré — c'est la frontière de la garde de propriété"},
	{"uptime, cpu, mémoire consommée", "des mesures, pas une configuration"},
	{"vmgenid, smbios, meta", "générés par PVE à la création, jamais déclarés"},
	{"disques hors du bloc « disk »", "le lecteur cloud-init (ide2), l'EFI et le TPM sont posés par le provider ailleurs que dans « disk »"},
}

// Kinds of finding. The three are distinct problems with distinct remedies,
// which is why they are not merged into one "difference" list.
const (
	// KindModified: declared and live disagree. Someone wrote outside Terraform.
	KindModified = "modifié"
	// KindOrphan: in the state, absent from the node. The resource was
	// destroyed by hand — terraform will try to update something gone.
	KindOrphan = "orphelin"
	// KindUnmanaged: on the node, absent from the state. Nothing owns it.
	KindUnmanaged = "non géré"
)

// Difference is one attribute that does not match.
type Difference struct {
	Field    string `json:"field"`
	Declared string `json:"declared"`
	Live     string `json:"live"`
}

// Finding is one guest that is not where it should be.
type Finding struct {
	Kind    string `json:"kind"`
	VMID    int    `json:"vmid"`
	Name    string `json:"name,omitempty"`
	Address string `json:"terraform_address,omitempty"`

	Differences []Difference `json:"differences,omitempty"`
}

// Report is the whole comparison.
type Report struct {
	Findings []Finding `json:"findings"`
}

// HasDrift drives the exit code: 0 when the declared and the real agree, 1
// when they do not. That is what makes `pvecli iac drift` usable as a
// scheduled job — drift is only a problem you fix if you are told about it.
func (r Report) HasDrift() bool { return len(r.Findings) > 0 }

// Only keeps a single category.
func (r Report) Only(kind string) Report {
	var out Report
	for _, f := range r.Findings {
		if f.Kind == kind {
			out.Findings = append(out.Findings, f)
		}
	}
	return out
}

// Compare confronts what Terraform declares with what the node reports.
func Compare(declared []Declared, live []Live) Report {
	byVMID := make(map[int]Live, len(live))
	for _, l := range live {
		byVMID[l.VMID] = l
	}
	inState := make(map[int]bool, len(declared))

	var report Report
	for _, d := range declared {
		inState[d.VMID] = true

		l, ok := byVMID[d.VMID]
		if !ok {
			report.Findings = append(report.Findings, Finding{
				Kind: KindOrphan, VMID: d.VMID, Name: d.Name, Address: d.Address,
			})
			continue
		}
		if diffs := diff(d, l); len(diffs) > 0 {
			report.Findings = append(report.Findings, Finding{
				Kind: KindModified, VMID: d.VMID, Name: d.Name, Address: d.Address,
				Differences: diffs,
			})
		}
	}

	for _, l := range live {
		if !inState[l.VMID] {
			report.Findings = append(report.Findings, Finding{
				Kind: KindUnmanaged, VMID: l.VMID, Name: l.Name,
			})
		}
	}

	sort.Slice(report.Findings, func(i, j int) bool {
		if report.Findings[i].VMID != report.Findings[j].VMID {
			return report.Findings[i].VMID < report.Findings[j].VMID
		}
		return report.Findings[i].Kind < report.Findings[j].Kind
	})
	return report
}

// diff compares one declared resource with its live counterpart.
func diff(d Declared, l Live) []Difference {
	var out []Difference
	add := func(field, declared, live string) {
		if declared != live {
			out = append(out, Difference{Field: field, Declared: declared, Live: live})
		}
	}

	// A name Terraform does not declare is a name it does not own: the provider
	// leaves whatever the clone produced, and reporting a difference would be
	// reporting on an attribute nobody claimed.
	if d.Name != "" {
		add("name", d.Name, l.Name)
	}
	if d.Cores != 0 {
		// PVE's `cores` is cores PER SOCKET, and so is the provider's
		// cpu.cores. Comparing them directly is correct; comparing either to
		// the guest's total vCPU count would not be.
		add("cores", strconv.Itoa(d.Cores), strconv.Itoa(l.Cores))
	}
	if d.Memory != 0 {
		add("memory", fmt.Sprintf("%d Mio", d.Memory), fmt.Sprintf("%d Mio", l.Memory))
	}
	if d.OnBoot != nil {
		add("on_boot", boolLabel(*d.OnBoot), boolLabel(l.OnBoot != nil && *l.OnBoot))
	}
	if len(d.Tags) > 0 {
		// PVE lowercases and reorders tags on write, so the comparison is on
		// sets, not on the strings as typed. A drift reported because the node
		// sorted a list differently would be a false positive nobody could act
		// on.
		add("tags", strings.Join(normalise(d.Tags), ","), strings.Join(normalise(l.Tags), ","))
	}

	for _, disk := range d.Disks {
		live, ok := l.Disks[disk.Interface]
		if !ok {
			add("disk "+disk.Interface, fmt.Sprintf("%d Gio sur %s", disk.Size, disk.Datastore), "absent")
			continue
		}
		if disk.Datastore != "" {
			add("disk "+disk.Interface+" datastore", disk.Datastore, live.Datastore)
		}
		if disk.Size != 0 {
			add("disk "+disk.Interface+" taille", fmt.Sprintf("%d Gio", disk.Size), fmt.Sprintf("%d Gio", live.SizeGiB))
		}
	}

	// network_device is an ordered list, and PVE names its interfaces net0,
	// net1… in the same order. Only the declared ones are compared: an
	// interface PVE has and Terraform does not describe is not a drift of a
	// declared attribute.
	for i, nic := range d.Networks {
		name := fmt.Sprintf("net%d", i)
		if i >= len(l.Networks) {
			add(name, nic.Bridge, "absente")
			continue
		}
		live := l.Networks[i]
		if nic.Bridge != "" {
			add(name+" bridge", nic.Bridge, live.Bridge)
		}
		if nic.Model != "" {
			add(name+" model", nic.Model, live.Model)
		}
		if nic.VLAN != 0 {
			add(name+" vlan", strconv.Itoa(nic.VLAN), strconv.Itoa(live.VLAN))
		}
	}
	return out
}

func boolLabel(b bool) string {
	if b {
		return "oui"
	}
	return "non"
}

func normalise(tags []string) []string {
	out := make([]string, 0, len(tags))
	for _, t := range tags {
		if t = strings.ToLower(strings.TrimSpace(t)); t != "" {
			out = append(out, t)
		}
	}
	sort.Strings(out)
	return out
}

// LiveFromPVE reduces a guest's real configuration to what Terraform declares.
//
// This is where the two vocabularies are reconciled, and every line of it is a
// schema fact that no documentation states side by side: PVE calls a container's
// name `hostname`, stores tags semicolon-separated, expresses a disk as an
// option string, and writes onboot as 0/1.
func LiveFromPVE(r pve.Resource, cfg pve.GuestConfig) Live {
	l := Live{
		VMID:  r.VMID,
		Name:  firstNonEmpty(cfg.String("name"), cfg.String("hostname"), r.Name),
		Node:  r.Node,
		Tags:  splitTags(firstNonEmpty(cfg.String("tags"), r.Tags)),
		Disks: map[string]LiveDisk{},
	}

	l.Cores, _ = strconv.Atoi(cfg.String("cores"))
	l.Memory, _ = strconv.Atoi(cfg.String("memory"))

	// onboot is absent from the configuration when it was never set, which is
	// not the same as being set to 0 — Terraform declaring on_boot = false on a
	// guest whose key is missing is not a drift.
	if v := cfg.String("onboot"); v != "" {
		b := v == "1"
		l.OnBoot = &b
	}

	for _, key := range diskKeys(cfg) {
		opt := pve.ParseOptionString(cfg.String(key))
		datastore, _, _ := strings.Cut(opt.Value, ":")
		l.Disks[key] = LiveDisk{Datastore: datastore, SizeGiB: sizeToGiB(opt.Get("size"))}
	}

	for _, key := range cfg.KeysWithPrefix("net") {
		opt := pve.ParseOptionString(cfg.String(key))
		nic := LiveNIC{Bridge: opt.Get("bridge")}
		// The model is the KEY of the leading pair — `net0: virtio=AA:BB:…` —
		// not a value under a "model" key. There is no other way to read it.
		for _, k := range opt.Keys() {
			switch k {
			case "virtio", "e1000", "rtl8139", "vmxnet3":
				nic.Model = k
			}
		}
		nic.VLAN, _ = strconv.Atoi(opt.Get("tag"))
		l.Networks = append(l.Networks, nic)
	}
	return l
}

// diskKeys returns the configuration keys that hold a disk, in a stable order.
func diskKeys(cfg pve.GuestConfig) []string {
	var keys []string
	for _, prefix := range []string{"scsi", "virtio", "sata", "ide", "rootfs", "mp"} {
		keys = append(keys, cfg.KeysWithPrefix(prefix)...)
	}
	// rootfs carries no index, so KeysWithPrefix — which requires one — misses
	// it. A container's only disk would otherwise never be compared.
	if cfg.String("rootfs") != "" {
		keys = append(keys, "rootfs")
	}
	sort.Strings(keys)
	return keys
}

// sizeToGiB reads PVE's disk size, which carries its unit as a suffix.
func sizeToGiB(size string) int {
	if size == "" {
		return 0
	}
	unit := size[len(size)-1]
	n, err := strconv.Atoi(strings.TrimRight(size, "KMGTkmgt"))
	if err != nil {
		return 0
	}
	switch unit {
	case 'T', 't':
		return n * 1024
	case 'G', 'g':
		return n
	case 'M', 'm':
		return n / 1024
	default:
		return 0
	}
}

func splitTags(tags string) []string {
	if tags == "" {
		return nil
	}
	return strings.Split(tags, ";")
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
