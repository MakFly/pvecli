package pve

import (
	"net/url"
	"strconv"
	"strings"
)

// CTOptions are the parameters of a container creation, before translation
// into the option strings PVE stores.
//
// Schema verified on the lab node (PVE 9.2.2) with `pvesh usage /nodes/pve/lxc
// -v`. Two fields do not exist on the QEMU side and carry the whole point of
// chapter 03: `ostemplate`, and `unprivileged`.
type CTOptions struct {
	Hostname   string
	OSTemplate string
	Cores      int
	Memory     int
	Swap       int
	RootFS     string
	Bridge     string
	IP         string
	Gateway    string
	Nameserver string
	OSType     string
	Tags       string
	OnBoot     bool

	// Password and SSHKeys never come from a command-line argument: see
	// cmd/lxc.go. They are carried here only to reach the payload.
	Password string
	SSHKeys  string

	// Privileged is stated the wrong way round on purpose. PVE's parameter is
	// `unprivileged`, but a zero-valued Go struct would then mean "privileged",
	// and the safe default would depend on somebody remembering to set it.
	// Reversed, the zero value IS the safe default.
	//
	// What the flag actually changes: an unprivileged container maps its UIDs
	// into a high, unused range of the host (root inside is uid 100000 outside).
	// A process escaping the container lands on the host as a user that owns
	// nothing. In a privileged container, root inside is root outside — the
	// container boundary becomes the only thing between the two.
	Privileged bool
}

// Values renders the options as the payload that will be sent. --dry-run shows
// this, unchanged: the translation from flags to option strings is the part
// worth seeing.
func (o CTOptions) Values() url.Values {
	v := url.Values{}
	v.Set("ostemplate", o.OSTemplate)

	if o.Hostname != "" {
		v.Set("hostname", o.Hostname)
	}
	if o.Cores > 0 {
		v.Set("cores", strconv.Itoa(o.Cores))
	}
	if o.Memory > 0 {
		v.Set("memory", strconv.Itoa(o.Memory))
	}
	if o.Swap > 0 {
		v.Set("swap", strconv.Itoa(o.Swap))
	}
	if o.RootFS != "" {
		v.Set("rootfs", o.RootFS)
	}
	if o.OSType != "" {
		v.Set("ostype", o.OSType)
	}
	if o.Nameserver != "" {
		v.Set("nameserver", o.Nameserver)
	}
	if o.Tags != "" {
		v.Set("tags", strings.ReplaceAll(o.Tags, ",", ";"))
	}
	if o.OnBoot {
		v.Set("onboot", "1")
	}
	if o.Password != "" {
		v.Set("password", o.Password)
	}
	if o.SSHKeys != "" {
		v.Set("ssh-public-keys", o.SSHKeys)
	}
	if net := o.NetDevice(); net != "" {
		v.Set("net0", net)
	}
	if !o.Privileged {
		v.Set("unprivileged", "1")
	}
	return v
}

// NetDevice builds the net0 option string.
//
// The shape differs from QEMU's, which is the trap of PVX-014: a VM interface
// starts with a positional model ("virtio,bridge=vmbr0"), a container one is
// already a key=value list and REQUIRES `name` — the interface name inside the
// guest. Omit it and PVE answers "missing property".
func (o CTOptions) NetDevice() string {
	if o.Bridge == "" {
		return ""
	}
	parts := []string{"name=eth0", "bridge=" + o.Bridge}
	if o.IP != "" {
		parts = append(parts, "ip="+o.IP)
	}
	if o.Gateway != "" {
		parts = append(parts, "gw="+o.Gateway)
	}
	return strings.Join(parts, ",")
}

// TemplateStorage returns the storage part of a vztmpl volid, so the pre-read
// can look for the template where it is actually declared.
//
// A volid is "storage:type/file", never a filesystem path — cutting on the
// first ':' is what the rest of the API does too.
func TemplateStorage(volid string) string {
	storage, _, ok := strings.Cut(volid, ":")
	if !ok {
		return ""
	}
	return storage
}
