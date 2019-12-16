package main

import (
	"encoding/base64"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"

	"github.com/pkg/errors"
)

func main() {
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Println("usage: monolith url")
		os.Exit(1)
	}
	u := flag.Arg(0)

	if err := monolith(u); err != nil {
		log.Fatal(err)
	}
}

func monolith(u string) error {
	// Download the page at the url.
	bod, err := get(u)
	bod = inline(u, bod)
	_, err = os.Stdout.Write(bod)
	if err != nil {
		return errors.Wrap(err, "outputting converted result")
	}

	return nil
}

// inline downloads and injects the contents of all linked resources in
// the html in h, some of which may be relative to the url u.
func inline(u string, h []byte) []byte {
	h = inlineImages(u, h)
	h = inlineStyles(u, h)
	h = inlineScripts(u, h)
	return h
}

func inlineImages(u string, h []byte) []byte {
	imgRx := regexp.MustCompile(`<img[^>]*>`)
	h = imgRx.ReplaceAllFunc(h, func(tag []byte) []byte {
		srcRx := regexp.MustCompile(`src="([^"]*)"`)
		return srcRx.ReplaceAllFunc(tag, func(src []byte) []byte {
			content := src[len(`src="`) : len(src)-1]
			base64Rx := regexp.MustCompile(`^data:image/\w+;base64`)
			if base64Rx.Match(content) {
				return src
			}
			imgURL := string(content)
			if !strings.HasPrefix(imgURL, "http") {
				tailRx := regexp.MustCompile(`/[^/]*$`)
				dir := tailRx.ReplaceAllString(u, "")
				imgURL = dir + "/" + imgURL
			}
			imgBytes, err := get(imgURL)
			if err != nil {
				log.Printf("%s", err)
				return src
			}
			src2 := []byte(`src="`)
			if strings.Contains(imgURL, ".jpg") {
				src2 = append(src2, []byte(`data:image/jpeg;base64,`)...)
			} else if strings.Contains(imgURL, ".png") {
				src2 = append(src2, []byte(`data:image/png;base64,`)...)
			} else if strings.Contains(imgURL, ".svg") {
				src2 = append(src2, []byte(`data:image/svg;base64,`)...)
			} else {
				log.Printf("Skpping image in unrecognized format: %s", imgURL)
				return src
			}
			// TODO(ijt): use NewEncoder().
			b64 := []byte(base64.StdEncoding.EncodeToString(imgBytes))
			src2 = append(src2, b64...)
			src2 = append(src2, []byte(`"`)...)
			return src2
		})
	})
	return h
}

func inlineStyles(u string, h []byte) []byte {
	return h
}

func inlineScripts(u string, h []byte) []byte {
	return h
}

func get(u string) ([]byte, error) {
	resp, err := http.Get(u)
	if err != nil {
		return nil, errors.Wrap(err, "fetching url")
	}
	defer resp.Body.Close()
	bod, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, errors.Wrap(err, "reading body of page")
	}
	return bod, nil
}
