package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/once-human/desk-agent/internal/adult"
	"github.com/once-human/desk-agent/internal/config"
	"github.com/once-human/desk-agent/internal/hostname"
	"github.com/once-human/desk-agent/internal/nativehost"
	"github.com/once-human/desk-agent/internal/paths"
	"github.com/once-human/desk-agent/internal/review"
)

// cmdNativeHost is what the browser launches (through the wrapper that
// install-native-host writes). It must print nothing to stdout except
// native-messaging replies, and we send none.
func cmdNativeHost(args []string) {
	_ = nativehost.Run(os.Stdin, paths.BrowserSocket(), nativehost.FamilyFromArgs(args))
}

func cmdInstallNativeHost(args []string) error {
	fs := flag.NewFlagSet("install-native-host", flag.ContinueOnError)
	all := fs.Bool("all", false, "write manifests for every known browser, installed or not")
	binary := fs.String("binary", "", "path to the desk-agent binary (default: this one)")
	remove := fs.Bool("remove", false, "remove the manifests instead")
	if err := fs.Parse(args); err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	if *remove {
		removed, _ := nativehost.Remove(home, paths.DataDir())
		for _, p := range removed {
			fmt.Println("removed", p)
		}
		if len(removed) == 0 {
			fmt.Println("nothing to remove")
		}
		return nil
	}
	var bargs []string
	if *binary != "" {
		bargs = []string{"--binary", *binary}
	}
	bin, err := binaryPath(bargs)
	if err != nil {
		return err
	}
	written, err := nativehost.Install(home, paths.DataDir(), bin, *all)
	if err != nil {
		return err
	}
	if len(written) == 0 {
		fmt.Println("no supported browser profile directory found; use --all to write manifests anyway")
		return nil
	}
	for _, p := range written {
		fmt.Println("wrote", p)
	}
	fmt.Println("Now load the extension: see extension/README.md")
	return nil
}

// defaultBlocklistURL is a community hosts-format list of adult domains
// (StevenBlack/hosts, "porn-only" extension). It is fetched only when the user
// runs `desk-agent fetch-blocklist`, never automatically.
const defaultBlocklistURL = "https://raw.githubusercontent.com/StevenBlack/hosts/master/alternates/porn-only/hosts"

// cmdFetchBlocklist downloads a hosts-format blocklist once, keeps only valid
// host lines, and writes them (one per line) to the adult blocklist file.
func cmdFetchBlocklist(args []string) error {
	fs := flag.NewFlagSet("fetch-blocklist", flag.ContinueOnError)
	url := fs.String("url", defaultBlocklistURL, "hosts-format list to download")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(paths.ConfigFile())
	if err != nil {
		return err
	}
	if !strings.HasPrefix(*url, "https://") {
		return errors.New("the blocklist URL must be https://")
	}
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Get(*url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("download failed: HTTP %d", resp.StatusCode)
	}
	f := adult.New()
	if err := f.Read(io.LimitReader(resp.Body, 32<<20)); err != nil {
		return err
	}
	if f.Len() == 0 {
		return errors.New("the list contained no usable hosts; nothing written")
	}
	var b strings.Builder
	b.WriteString("# Adult-site blocklist, fetched by `desk-agent fetch-blocklist`.\n# Matching hosts (and their subdomains) are never named.\n")
	for _, h := range f.Hosts() {
		if _, ok := hostname.Normalise(h); ok {
			b.WriteString(h + "\n")
		}
	}
	if err := review.WriteAtomic(cfg.AdultBlocklistFile, []byte(b.String())); err != nil {
		return err
	}
	fmt.Printf("Wrote %d hosts to %s\n", f.Len(), cfg.AdultBlocklistFile)
	notifyReload()
	return nil
}
