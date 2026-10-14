package main

import (
	"flag"
	"log"
	"net/http"
	"net/url"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// Bounded native lifecycle spike, not a production reader.
func main() {
	endpoint := flag.String("url", "", "Loopback fixture URL")
	closeAfter := flag.Duration("close-after", 0, "Close the fixture window after this interval")
	flag.Parse()
	u, err := url.Parse(*endpoint)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.User != nil || u.Port() == "" {
		log.Fatal("http://127.0.0.1:<port> fixture URL required")
	}
	app := application.New(application.Options{
		Name:   "Haeryong Stack Spike",
		Mac:    application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: true},
		Assets: application.AssetOptions{Handler: http.NotFoundHandler()},
	})
	window := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title: "Haeryong stack spike", Width: 700, Height: 500, URL: *endpoint,
	})
	if *closeAfter > 0 {
		go func() { time.Sleep(*closeAfter); window.Close() }()
	}
	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
