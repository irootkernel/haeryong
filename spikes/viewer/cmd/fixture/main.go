// Disposable G-VIEW receiver; no production import, Notes, Ask or Review.
package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	viewer "haeryong.local/viewer-spike"
)

func main() {
	root := flag.String("state", "", "Owned private fixture directory")
	base := flag.String("base", "http://127.0.0.1:9270", "Trusted local origin")
	remote := flag.String("remote", "", "Exact private trusted HTTPS origin")
	a := flag.String("renderer-a", "http://127.0.0.1:9272", "Renderer A origin")
	b := flag.String("renderer-b", "http://127.0.0.1:9273", "Renderer B origin")
	remoteA := flag.String("remote-a", "", "Exact private renderer A origin")
	remoteB := flag.String("remote-b", "", "Exact private renderer B origin")
	flag.Parse()
	fixture, err := viewer.New(viewer.Config{State: *root, Base: *base, Remote: *remote, Renderers: [2]string{*a, *b}, RemoteRenderers: [2]string{*remoteA, *remoteB}})
	if err != nil {
		log.Fatal(err)
	}
	if err = fixture.Start(); err != nil {
		log.Fatal(err)
	}
	defer fixture.Close()
	log.Print("viewer fixture ready; credentials remain in private state")
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
}
