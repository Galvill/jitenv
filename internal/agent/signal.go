package agent

import (
	"os"
	"os/signal"
)

func signalRegister(ch chan os.Signal, sigs ...os.Signal) {
	signal.Notify(ch, sigs...)
}
