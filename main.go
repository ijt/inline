package main

import (
	"encoding/base64"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"github.com/pkg/errors"
)

func main() {
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Println("usage: inline url")
		os.Exit(1)
	}
	u := flag.Arg(0)

	if err := app(u); err != nil {
		log.Fatal(err)
	}
}

func app(u string) error {
	// Download the page at the url.
	bod, err := fetchDOM(u)
	if err != nil {
		log.Println("dumping the DOM with Chrome failed, falling back on Go HTTP client")
		bod, err = get(u)
		if err != nil {
			return errors.Wrap(err, "fetching page")
		}
	}
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
		// TODO(ijt): Make this better:
		src := getSrc(tag)
		if src == "" {
			log.Printf("img tag has no src field: %s", tag)
			return tag
		}
		if strings.Contains(src, ".svg") {
			svgURL, err := resolve(u, src)
			if err != nil {
				log.Println("failed: ", err)
				return tag
			}
			svg, err := get(svgURL)
			if err != nil {
				log.Println("failed: ", err)
				return tag
			}
			log.Printf("inlining %s", svgURL)
			return svg
		}
		return srcRx.ReplaceAllFunc(tag, func(src []byte) []byte {
			content := src[len(`src="`) : len(src)-1]
			base64Rx := regexp.MustCompile(`^data:image/\w+;base64`)
			if base64Rx.Match(content) {
				return src
			}
			imgURL := string(content)
			imgURL, err := resolve(u, imgURL)
			if err != nil {
				log.Println("failed: ", err)
				return src
			}
			imgBytes, typ, err := getWithMime(imgURL)
			if err != nil {
				log.Println(err)
				return src
			}
			src2 := []byte(`src="`)
			src2 = append(src2, []byte(fmt.Sprintf(`data:%s;base64,`, typ))...)
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
	styleRx := regexp.MustCompile(`<link [^>]*rel="stylesheet"[^>]*>`)
	return styleRx.ReplaceAllFunc(h, func(tag []byte) []byte {
		cssURL := getHref(tag)
		if cssURL == "" {
			log.Printf("no href found in tag %s", tag)
			return tag
		}
		cssURL, err := resolve(u, cssURL)
		if err != nil {
			log.Println("failed: ", err)
			return tag
		}
		log.Printf("inlining CSS from %s", cssURL)
		css, err := get(cssURL)
		if err != nil {
			log.Printf("fetching CSS at %s: %s", cssURL, err)
			return tag
		}
		cssElt := fmt.Sprintf(`
			<style>
				%s
			</style>
		`, css)
		return []byte(cssElt)
	})
}

func inlineScripts(u string, h []byte) []byte {
	scriptRx := regexp.MustCompile(`<script [^>]*(></script>|/>)`)
	return scriptRx.ReplaceAllFunc(h, func(tag []byte) []byte {
		scriptURL := getSrc(tag)
		if scriptURL == "" {
			log.Printf("no href found in tag %s", tag)
			return tag
		}
		scriptURL, err := resolve(u, scriptURL)
		if err != nil {
			log.Println("failed: ", err)
			return tag
		}
		log.Printf("inlining script from %s", scriptURL)
		script, err := get(scriptURL)
		if err != nil {
			log.Printf("fetching script at %s: %s", scriptURL, err)
			return tag
		}
		elt := fmt.Sprintf(`
			<!-- %s -->
			<script>
				%s
			</script>
		`, scriptURL, script)
		return []byte(elt)
	})
}

func get(u string) ([]byte, error) {
	bod, _, err := getWithMime(u)
	return bod, err
}

func getWithMime(u string) ([]byte, string, error) {
	resp, err := http.Get(u)
	if err != nil {
		return nil, "", errors.Wrap(err, "fetching url")
	}
	defer resp.Body.Close()
	bod, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, "", errors.Wrap(err, "reading body of page")
	}
	typ := resp.Header.Get("content-type")
	return bod, typ, nil
}

// resolve makes the url r relative to baseURL if r is a relative URL.
func resolve(baseURL, r string) (string, error) {
	u, err := url.Parse(r)
	if err != nil {
		return "", errors.Wrap(err, "parsing possibly relative URL")
	}
	base, err := url.Parse(baseURL)
	if err != nil {
		return "", errors.Wrap(err, "parsing base URL")
	}
	abs := base.ResolveReference(u)
	return abs.String(), nil
}

func getHref(tag []byte) string {
	hrefRx := regexp.MustCompile(`href="[^"]*"`)
	hrefEqn := string(hrefRx.Find(tag))
	if !strings.Contains(hrefEqn, `href="`) {
		return ""
	}
	return string(hrefEqn[len(`href="`) : len(hrefEqn)-1])
}

func getSrc(tag []byte) string {
	srcRx := regexp.MustCompile(`src="[^"]*"`)
	srcEqn := string(srcRx.Find(tag))
	if !strings.Contains(srcEqn, `src="`) {
		return ""
	}
	return string(srcEqn[len(`src="`) : len(srcEqn)-1])
}

func fetchDOM(u string) ([]byte, error) {
	cmd := exec.Command("/Applications/Google Chrome.app/Contents/MacOS/Google Chrome", "--headless", "--dump-dom", u)
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, errors.Wrapf(err, "dumping page DOM with Chrome")
	}
	return out, nil
}
