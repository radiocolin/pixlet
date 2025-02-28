// Package loader provides primitives to load an applet both when the underlying
// file changes and on demand when an update is requested.
package loader

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"

	"tidbyt.dev/pixlet/encode"
	"tidbyt.dev/pixlet/runtime"
	"tidbyt.dev/pixlet/schema"
)

// Loader is a structure to provide applet loading when a file changes or on
// demand.
type Loader struct {
	filename         string
	fileChanges      chan bool
	watch            bool
	applet           runtime.Applet
	configChanges    chan map[string]string
	requestedChanges chan bool
	updatesChan      chan Update
	resultsChan      chan Update
	maxDuration      int
}

type Update struct {
	WebP   string
	Schema string
	Err    error
}

// NewLoader instantiates a new loader structure. The loader will read off of
// fileChanges channel and write updates to the updatesChan. Updates are base64
// encoded WebP strings. If watch is enabled, both file changes and on demand
// requests will send updates over the updatesChan.
func NewLoader(
	filename string,
	watch bool,
	fileChanges chan bool,
	updatesChan chan Update,
	maxDuration int,
) (*Loader, error) {

	l := &Loader{
		filename:         filename,
		fileChanges:      fileChanges,
		watch:            watch,
		applet:           runtime.Applet{},
		updatesChan:      updatesChan,
		configChanges:    make(chan map[string]string, 100),
		requestedChanges: make(chan bool, 100),
		resultsChan:      make(chan Update, 100),
		maxDuration:      maxDuration,
	}

	cache := runtime.NewInMemoryCache()
	runtime.InitHTTP(cache)
	runtime.InitCache(cache)

	if !l.watch {
		err := loadScript(&l.applet, l.filename)
		if err != nil {
			return nil, err
		}
	}

	return l, nil
}

// Run executes the main loop. If there are config changes, those are recorded.
// If there is an on-demand request, it's processed and sent back to the caller
// and sent out as an update. If there is a file change, we update the applet
// and send out the update over the updatesChan.
func (l *Loader) Run() error {
	config := make(map[string]string)

	for {
		select {
		case c := <-l.configChanges:
			config = c
		case <-l.requestedChanges:
			up := Update{}

			webp, err := l.loadApplet(config)
			if err != nil {
				log.Printf("error loading applet: %v", err)
				up.Err = err
			} else {
				up.WebP = webp
			}

			l.updatesChan <- up
			l.resultsChan <- up
		case <-l.fileChanges:
			log.Printf("detected updates for %s, reloading\n", l.filename)
			up := Update{}

			webp, err := l.loadApplet(config)
			if err != nil {
				log.Printf("error loading applet: %v", err)
				up.Err = err
			} else {
				up.WebP = webp
				up.Schema = l.applet.GetSchema()
			}

			l.updatesChan <- up
		}
	}
}

// LoadApplet loads the applet on demand.
//
// TODO: This method is thread safe, but has a pretty glaring race condition. If
// two callers request an update at the same time, they have the potential to
// get each others update. At the time of writing, this method is only called
// when you refresh a webpage during app development - so it doesn't seem likely
// that it's going to cause issues in the short term.
func (l *Loader) LoadApplet(config map[string]string) (string, error) {
	l.configChanges <- config
	l.requestedChanges <- true
	result := <-l.resultsChan
	return result.WebP, result.Err
}

func (l *Loader) GetSchema() []byte {
	s := []byte(l.applet.GetSchema())
	if len(s) > 0 {
		return s
	}

	b, _ := json.Marshal(&schema.Schema{})
	return b
}

func (l *Loader) CallSchemaHandler(ctx context.Context, handlerName, parameter string) (string, error) {
	return l.applet.CallSchemaHandler(ctx, handlerName, parameter)
}

func (l *Loader) loadApplet(config map[string]string) (string, error) {
	if l.watch {
		err := loadScript(&l.applet, l.filename)
		if err != nil {
			return "", err
		}
	}

	roots, err := l.applet.Run(config)
	if err != nil {
		return "", fmt.Errorf("error running script: %w", err)
	}

	screens := encode.ScreensFromRoots(roots)

	maxDuration := l.maxDuration
	if screens.ShowFullAnimation {
		maxDuration = 0
	}
	webp, err := screens.EncodeWebP(maxDuration)
	if err != nil {
		return "", fmt.Errorf("error rendering: %w", err)
	}

	return base64.StdEncoding.EncodeToString(webp), nil
}

func loadScript(applet *runtime.Applet, filename string) error {
	src, err := ioutil.ReadFile(filename)
	if err != nil {
		return fmt.Errorf("failed to read file %s: %w", filename, err)
	}

	err = applet.Load(filename, src, nil)
	if err != nil {
		return fmt.Errorf("failed to load applet: %w", err)
	}

	return nil
}
