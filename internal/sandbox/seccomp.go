package sandbox

import (
	"encoding/binary"
	"fmt"
	"os"
	"runtime"
)

const (
	bpfLD  = 0x00
	bpfW   = 0x00
	bpfABS = 0x20

	bpfJMP = 0x05
	bpfJEQ = 0x10
	bpfK   = 0x00

	bpfRET = 0x06

	seccompDataNROffset   = 0
	seccompDataArchOffset = 4

	seccompRetErrno = 0x00050000
	seccompRetAllow = 0x7fff0000

	linuxEPERM = 1

	auditArchX8664   = 0xc000003e
	auditArchAArch64 = 0xc00000b7
)

type sockFilter struct {
	code uint16
	jt   uint8
	jf   uint8
	k    uint32
}

func createSeccompProfileFile() (*os.File, func() error, error) {
	program, err := seccompBPFProgram(runtime.GOARCH)
	if err != nil {
		return nil, nil, err
	}
	file, err := os.CreateTemp("", "niuma-seccomp-*.bpf")
	if err != nil {
		return nil, nil, err
	}
	remove := func() error {
		return os.Remove(file.Name())
	}
	if _, err := file.Write(program); err != nil {
		_ = file.Close()
		_ = remove()
		return nil, nil, err
	}
	if _, err := file.Seek(0, 0); err != nil {
		_ = file.Close()
		_ = remove()
		return nil, nil, err
	}
	return file, remove, nil
}

func seccompBPFProgram(goarch string) ([]byte, error) {
	arch, syscalls, ok := seccompProfileForArch(goarch)
	if !ok {
		return nil, fmt.Errorf("seccomp BPF profile is unavailable for GOARCH=%s", goarch)
	}
	filters := []sockFilter{
		stmt(bpfLD|bpfW|bpfABS, seccompDataArchOffset),
		jump(bpfJMP|bpfJEQ|bpfK, arch, 1, 0),
		stmt(bpfRET|bpfK, seccompRetErrno|linuxEPERM),
		stmt(bpfLD|bpfW|bpfABS, seccompDataNROffset),
	}
	for _, nr := range syscalls {
		filters = append(filters,
			jump(bpfJMP|bpfJEQ|bpfK, nr, 0, 1),
			stmt(bpfRET|bpfK, seccompRetErrno|linuxEPERM),
		)
	}
	filters = append(filters, stmt(bpfRET|bpfK, seccompRetAllow))
	out := make([]byte, 0, len(filters)*8)
	for _, filter := range filters {
		var encoded [8]byte
		binary.LittleEndian.PutUint16(encoded[0:2], filter.code)
		encoded[2] = filter.jt
		encoded[3] = filter.jf
		binary.LittleEndian.PutUint32(encoded[4:8], filter.k)
		out = append(out, encoded[:]...)
	}
	return out, nil
}

func stmt(code uint16, k uint32) sockFilter {
	return sockFilter{code: code, k: k}
}

func jump(code uint16, k uint32, jt, jf uint8) sockFilter {
	return sockFilter{code: code, jt: jt, jf: jf, k: k}
}

func seccompProfileForArch(goarch string) (uint32, []uint32, bool) {
	switch goarch {
	case "amd64":
		return auditArchX8664, []uint32{
			101, // ptrace
			155, // pivot_root
			161, // chroot
			163, // acct
			165, // mount
			166, // umount2
			167, // swapon
			168, // swapoff
			169, // reboot
			175, // init_module
			176, // delete_module
			246, // kexec_load
			248, // add_key
			249, // request_key
			250, // keyctl
			272, // unshare
			298, // perf_event_open
			300, // fanotify_init
			304, // open_by_handle_at
			308, // setns
			313, // finit_module
			321, // bpf
			435, // clone3
		}, true
	case "arm64":
		return auditArchAArch64, []uint32{
			39,  // umount2
			40,  // mount
			41,  // pivot_root
			51,  // chroot
			89,  // acct
			97,  // unshare
			104, // kexec_load
			105, // init_module
			106, // delete_module
			117, // ptrace
			142, // reboot
			217, // add_key
			218, // request_key
			219, // keyctl
			224, // swapon
			225, // swapoff
			241, // perf_event_open
			262, // fanotify_init
			265, // open_by_handle_at
			268, // setns
			273, // finit_module
			280, // bpf
			435, // clone3
		}, true
	default:
		return 0, nil, false
	}
}
