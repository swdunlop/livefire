// +build !windows

package tarantula

import (
	"os"
	"os/signal"
	"syscall"
)

// isolated from tarantula.go for windows' protection
func (svc *Service) handleSignals() {
	defer svc.Stop()
	done := make(chan os.Signal)
	signal.Notify(done, syscall.SIGUSR1)
	<-done
}
