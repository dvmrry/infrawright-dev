//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package terraformcmd

import (
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
)

const terraformProcessGroupsSupported = true

var activeTerraformGroups = terraformProcessGroupRegistry{
	pids: make(map[int]struct{}),
}

type terraformProcessGroupRegistry struct {
	mu                 sync.Mutex
	pids               map[int]struct{}
	signals            chan os.Signal
	stop               chan struct{}
	done               chan struct{}
	terminating        bool
	terminationRelease chan struct{}
}

var terraformTerminationSignals = []os.Signal{
	syscall.SIGTERM,
	syscall.SIGINT,
	syscall.SIGHUP,
}

var terraformResignalProcess = func(received syscall.Signal) error {
	return syscall.Kill(os.Getpid(), received)
}

// terraformSignalReceivedHook is nil in production and gives the race tests a
// deterministic point after channel receipt but before registry locking.
var terraformSignalReceivedHook func()

func configureTerraformProcess(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killTerraformProcessGroup(pid int, process *os.Process) {
	if pid > 0 {
		if err := syscall.Kill(-pid, syscall.SIGKILL); err == nil {
			return
		}
	}
	if process != nil {
		_ = process.Kill()
	}
}

// startTerraformProcess installs the termination watcher before Start and
// holds the registry lock until the new process group is registered. Thus a
// signal delivered during Start cannot strand the just-created group in the
// gap between os/exec returning and registry insertion.
func startTerraformProcess(command *exec.Cmd) (func(), error) {
	activeTerraformGroups.mu.Lock()
	for activeTerraformGroups.terminating {
		release := activeTerraformGroups.terminationRelease
		activeTerraformGroups.mu.Unlock()
		<-release
		activeTerraformGroups.mu.Lock()
	}
	if activeTerraformGroups.signals == nil {
		signals := make(chan os.Signal, 1)
		stop := make(chan struct{})
		done := make(chan struct{})
		activeTerraformGroups.signals = signals
		activeTerraformGroups.stop = stop
		activeTerraformGroups.done = done
		signal.Notify(signals, terraformTerminationSignals...)
		go watchTerraformTerminationSignals(signals, stop, done)
	}
	if err := command.Start(); err != nil {
		var done <-chan struct{}
		if len(activeTerraformGroups.pids) == 0 {
			signals := activeTerraformGroups.signals
			stop := activeTerraformGroups.stop
			done = activeTerraformGroups.done
			activeTerraformGroups.signals = nil
			activeTerraformGroups.stop = nil
			activeTerraformGroups.done = nil
			signal.Stop(signals)
			close(stop)
		}
		activeTerraformGroups.mu.Unlock()
		if done != nil {
			<-done
		}
		return nil, err
	}
	pid := command.Process.Pid
	activeTerraformGroups.pids[pid] = struct{}{}
	activeTerraformGroups.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() { unregisterTerraformProcessGroup(pid) })
	}, nil
}

func unregisterTerraformProcessGroup(pid int) {
	activeTerraformGroups.mu.Lock()
	for activeTerraformGroups.terminating {
		release := activeTerraformGroups.terminationRelease
		activeTerraformGroups.mu.Unlock()
		<-release
		activeTerraformGroups.mu.Lock()
	}
	delete(activeTerraformGroups.pids, pid)
	if len(activeTerraformGroups.pids) != 0 || activeTerraformGroups.signals == nil {
		activeTerraformGroups.mu.Unlock()
		return
	}
	signals := activeTerraformGroups.signals
	stop := activeTerraformGroups.stop
	done := activeTerraformGroups.done
	activeTerraformGroups.signals = nil
	activeTerraformGroups.stop = nil
	activeTerraformGroups.done = nil
	signal.Stop(signals)
	close(stop)
	activeTerraformGroups.mu.Unlock()
	<-done
}

func watchTerraformTerminationSignals(signals <-chan os.Signal, stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)
	select {
	case received := <-signals:
		handleTerraformTerminationSignal(received, stop)
	case <-stop:
		// Teardown calls signal.Stop before closing stop, so no new sends can
		// arrive. Still drain a signal that was buffered first; consuming it
		// without re-sending would turn a real termination request into a no-op.
		select {
		case received := <-signals:
			handleTerraformTerminationSignal(received, stop)
		default:
		}
	}
}

func handleTerraformTerminationSignal(received os.Signal, generationStop <-chan struct{}) {
	if terraformSignalReceivedHook != nil {
		terraformSignalReceivedHook()
	}
	concrete, ok := received.(syscall.Signal)
	if !ok {
		return
	}

	activeTerraformGroups.mu.Lock()
	currentGeneration := activeTerraformGroups.stop == generationStop
	var pids []int
	var release chan struct{}
	if currentGeneration {
		pids = make([]int, 0, len(activeTerraformGroups.pids))
		for pid := range activeTerraformGroups.pids {
			pids = append(pids, pid)
		}
		// Prevent command goroutines awakened by SIGKILL from returning an
		// ordinary command failure (and allowing main to os.Exit(1)) before the
		// original termination signal is restored below.
		release = make(chan struct{})
		activeTerraformGroups.terminating = true
		activeTerraformGroups.terminationRelease = release
		clear(activeTerraformGroups.pids)
		registeredSignals := activeTerraformGroups.signals
		activeTerraformGroups.signals = nil
		activeTerraformGroups.stop = nil
		activeTerraformGroups.done = nil
		if registeredSignals != nil {
			signal.Stop(registeredSignals)
		}
	}
	activeTerraformGroups.mu.Unlock()

	for _, pid := range pids {
		killTerraformProcessGroup(pid, nil)
	}
	// Stop only this package's subscription. If another package subscribed to
	// the same signal, it must still receive the re-sent signal just as another
	// Node listener would after removeListener removes only our callback.
	_ = terraformResignalProcess(concrete)
	// With the default disposition the process terminates during or immediately
	// after the self-signal. If another host subscriber handles it, execution
	// continues just as it does with another Node listener; release commands only
	// after the re-signal attempt so they cannot win the pre-signal exit race.
	if release != nil {
		activeTerraformGroups.mu.Lock()
		if activeTerraformGroups.terminating && activeTerraformGroups.terminationRelease == release {
			activeTerraformGroups.terminating = false
			activeTerraformGroups.terminationRelease = nil
			close(release)
		}
		activeTerraformGroups.mu.Unlock()
	}
}
