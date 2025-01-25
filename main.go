package main

import (
	"coachify-account-api/app"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	// Créer une nouvelle application
	a := app.New()

	// Exécuter l'application dans une goroutine
	go func() {
		a.Run()
	}()

	// Capturer les signaux d'arrêt
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Attendre un signal d'arrêt
	<-sigChan

	// Appeler la méthode de shutdown
	a.Shutdown()

}
