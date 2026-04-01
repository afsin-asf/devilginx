package core

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/afsin-asf/devilginx/log"
	"github.com/fsnotify/fsnotify"
)

// InitPhishletWatcher starts watching the phishlets directory for changes
func (c *Config) InitPhishletWatcher(phishletsDir string) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("failed to create phishlet watcher: %v", err)
	}

	err = watcher.Add(phishletsDir)
	if err != nil {
		watcher.Close()
		return fmt.Errorf("failed to watch phishlets directory: %v", err)
	}

	log.Info("phishlets: started watching directory for changes: %s", phishletsDir)

	// Start a goroutine to handle file events
	go c.watchPhishletEvents(watcher, phishletsDir)

	return nil
}

// watchPhishletEvents handles file system events for the phishlets directory
func (c *Config) watchPhishletEvents(watcher *fsnotify.Watcher, phishletsDir string) {
	defer watcher.Close()

	// Track recently modified files to avoid multiple reloads
	recentlyModified := make(map[string]time.Time)

	for {
		select {
		case event, ok := <-watcher.Events:
			if !ok {
				return
			}

			// Only process YAML files
			if !strings.HasSuffix(event.Name, ".yaml") {
				continue
			}

			// Get the phishlet name from the filename
			filename := filepath.Base(event.Name)
			pr := regexp.MustCompile(`([a-zA-Z0-9\-\.]*)\.yaml`)
			matches := pr.FindStringSubmatch(filename)
			if matches == nil || len(matches) < 2 {
				continue
			}
			phishletName := matches[1]
			if phishletName == "" {
				continue
			}

			// Add debouncing to avoid multiple reloads for the same file
			if lastMod, exists := recentlyModified[event.Name]; exists {
				if time.Since(lastMod) < 500*time.Millisecond {
					continue
				}
			}
			recentlyModified[event.Name] = time.Now()

			switch event.Op {
			case fsnotify.Write, fsnotify.Create:
				log.Info("phishlets: detected change in '%s', reloading...", filename)
				c.reloadPhishlet(phishletName, event.Name)

			case fsnotify.Remove:
				log.Info("phishlets: detected removal of '%s', removing from cache...", filename)
				c.removePhishlet(phishletName)

			case fsnotify.Rename:
				// For renames, treat as remove old and add new
				log.Info("phishlets: detected rename of '%s'", filename)
				c.removePhishlet(phishletName)
			}

		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			log.Error("phishlets watcher error: %v", err)
		}
	}
}

// reloadPhishlet reloads a single phishlet from disk
func (c *Config) reloadPhishlet(phishletName string, filePath string) {
	// Try to load the phishlet
	newPhishlet, err := NewPhishlet(phishletName, filePath, nil, c)
	if err != nil {
		log.Error("phishlets: failed to reload '%s': %v", phishletName, err)
		return
	}

	// Replace the existing phishlet
	c.phishlets[phishletName] = newPhishlet

	// Verify phishlets to check for conflicts
	c.VerifyPhishlets()

	log.Info("phishlets: successfully reloaded '%s'", phishletName)
}

// removePhishlet removes a phishlet from the cache
func (c *Config) removePhishlet(phishletName string) {
	if _, ok := c.phishlets[phishletName]; ok {
		delete(c.phishlets, phishletName)

		// Update phishlet names list
		newNames := []string{}
		for _, name := range c.phishletNames {
			if name != phishletName {
				newNames = append(newNames, name)
			}
		}
		c.phishletNames = newNames

		log.Info("phishlets: removed '%s' from cache", phishletName)
	}
}
