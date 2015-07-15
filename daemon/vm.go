package daemon

import (
	"fmt"
	"strconv"

	"github.com/hyperhq/hyper/engine"
	"github.com/hyperhq/runv/hypervisor"
	"github.com/hyperhq/runv/hypervisor/pod"
	"github.com/hyperhq/runv/hypervisor/qemu"
	"github.com/hyperhq/runv/hypervisor/types"
	"github.com/hyperhq/runv/hypervisor/xen"
	"github.com/hyperhq/runv/lib/glog"
)

func DriversProbe() hypervisor.HypervisorDriver {
	xd := xen.InitDriver()
	if xd != nil {
		glog.Info("Xen Driver Loaded.")
		return xd
	}

	qd := &qemu.QemuDriver{}
	if err := qd.Initialize(); err == nil {
		glog.Info("Qemu Driver Loaded")
		return qd
	} else {
		glog.Info("Qemu Driver Load failed: ", err.Error())
	}

	glog.Error("No driver available")
	return nil
}

func (daemon *Daemon) CmdVmCreate(job *engine.Job) (err error) {
	var (
		vmId = fmt.Sprintf("vm-%s", pod.RandStr(10, "alpha"))
		cpu  = 1
		mem  = 128
	)

	if job.Args[0] != "" {
		cpu, err = strconv.Atoi(job.Args[0])
		if err != nil {
			return err
		}
	}

	if job.Args[1] != "" {
		mem, err = strconv.Atoi(job.Args[1])
		if err != nil {
			return err
		}
	}

	b := &hypervisor.BootConfig{
		CPU:    cpu,
		Memory: mem,
		Kernel: daemon.kernel,
		Initrd: daemon.initrd,
		Bios:   daemon.bios,
		Cbfs:   daemon.cbfs,
	}

	vm := daemon.NewVm(vmId, cpu, mem)

	err = vm.Launch(b)
	if err != nil {
		return err
	}

	daemon.AddVm(vm)

	// Prepare the qemu status to client
	v := &engine.Env{}
	v.Set("ID", vmId)
	v.SetInt("Code", 0)
	v.Set("Cause", "")
	if _, err := v.WriteTo(job.Stdout); err != nil {
		return err
	}

	return nil
}

func (daemon *Daemon) CmdVmKill(job *engine.Job) error {
	vmId := job.Args[0]
	if _, ok := daemon.vmList[vmId]; !ok {
		return fmt.Errorf("Can not find the VM(%s)", vmId)
	}
	code, cause, err := daemon.KillVm(vmId)
	if err != nil {
		return err
	}

	// Prepare the qemu status to client
	v := &engine.Env{}
	v.Set("ID", vmId)
	v.SetInt("Code", code)
	v.Set("Cause", cause)
	if _, err := v.WriteTo(job.Stdout); err != nil {
		return err
	}

	return nil
}

func (daemon *Daemon) KillVm(vmId string) (int, string, error) {
	vm, ok := daemon.vmList[vmId]
	if !ok {
		return 0, "", nil
	}
	ret1, ret2, err := vm.Kill()
	if err == nil {
		daemon.RemoveVm(vmId)
	}
	return ret1, ret2, err
}

// This function will only be invoked during daemon start
func (daemon *Daemon) AssociateAllVms() error {
	for _, mypod := range daemon.podList {
		if mypod.Vm == "" {
			continue
		}
		podData, err := daemon.GetPodByName(mypod.Id)
		if err != nil {
			continue
		}
		userPod, err := pod.ProcessPodBytes(podData)
		if err != nil {
			continue
		}
		glog.V(1).Infof("Associate the POD(%s) with VM(%s)", mypod.Id, mypod.Vm)

		vmData, err := daemon.GetVmData(mypod.Vm)
		if err != nil {
			continue
		}
		glog.V(1).Infof("The data for vm(%s) is %v", mypod.Vm, vmData)

		vm := daemon.NewVm(mypod.Vm, userPod.Resource.Vcpu, userPod.Resource.Memory)

		err = vm.AssociateVm(mypod, vmData)
		if err != nil {
			continue
		}

		daemon.AddVm(vm)
	}
	return nil
}

func (daemon *Daemon) ReleaseAllVms() (int, error) {
	var (
		ret       = types.E_OK
		err error = nil
	)

	for _, vm := range daemon.vmList {
		ret, err = vm.ReleaseVm()
		if err != nil {
			/* FIXME: continue to release other vms? */
			break
		}
	}

	return ret, err
}

func (daemon *Daemon) NewVm(id string, cpu, memory int) *hypervisor.Vm {
	vmId := id

	if vmId == "" {
		for {
			vmId = fmt.Sprintf("vm-%s", pod.RandStr(10, "alpha"))
			if _, ok := daemon.vmList[vmId]; !ok {
				break
			}
		}
	}
	return hypervisor.NewVm(vmId, cpu, memory)
}
