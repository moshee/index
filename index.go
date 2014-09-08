package main

import (
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"ktkr.us/pkg/gas"
	"ktkr.us/pkg/gas/out"
)

var Conf struct {
	Root string `default:"."`
}

func main() {
	err := gas.EnvConf(&Conf, "INDEX_")
	if err != nil {
		log.Fatal(err)
	}

	r := gas.New()
	r.StaticHandler()
	r.Get("{path}", getIndex)
	r.Ignition()
}

type ctx struct {
	Path string
	G    *gas.Gas
	Data interface{}
}

func getIndex(g *gas.Gas) (int, gas.Outputter) {
	p := filepath.Join(Conf.Root, g.URL.Path)
	c := &ctx{
		Path: g.URL.Path,
		G:    g,
	}

	fi, err := os.Stat(p)
	if err != nil {
		if os.IsNotExist(err) {
			return 404, out.HTML("404", c, "layout")
		} else {
			c.Data = err
			return 500, out.HTML("500", c, "layout")
		}
	}

	if !fi.IsDir() {
		http.ServeFile(g, g.Request, p)
		return g.Stop()
	}

	fis, err := ioutil.ReadDir(p)
	if err != nil {
		return 500, out.HTML("500", c, "layout")
	}

	entries := make([]*FileEntry, 0, len(fis))
	var readme []byte

	for _, fi := range fis {
		if strings.HasPrefix(fi.Name(), ".") {
			continue
		}
		path := filepath.Join(p, fi.Name())
		if isReadme(fi) {
			f, err := os.Open(path)
			if err != nil {
				readme = []byte(err.Error())
			} else {
				readme, err = ioutil.ReadAll(f)
				if err != nil {
					readme = []byte(err.Error())
				}
			}
		}
		e := &FileEntry{
			Component: Component{
				Name: fi.Name(),
				Path: filepath.Join(g.URL.Path, fi.Name()),
			},
			Size:     Bytes(fi.Size()),
			IsDir:    fi.IsDir(),
			IsLink:   fi.Mode()&os.ModeSymlink != 0,
			Mod:      fi.ModTime(),
			FileMode: fi.Mode(),
		}
		if fi.IsDir() {
			dir, err := os.Open(path)
			if err != nil {
				log.Print(err)
			} else {
				names, _ := dir.Readdirnames(-1)
				n := 0
				for _, name := range names {
					if !strings.HasPrefix(name, ".") {
						n++
					}
				}
				e.NumEntries = n
			}
		}
		entries = append(entries, e)
	}

	sortCol := g.FormValue("s")
	sortRev := false
	if sortCol != "" {
		var sorter sort.Interface

		switch sortCol {
		case "n":
			sorter = byName(entries)
		case "s":
			sorter = bySize(entries)
		case "m":
			sorter = byModTime(entries)
		default:
			goto a
		}

		sortRev, err = strconv.ParseBool(g.FormValue("r"))
		if sortRev {
			sorter = sort.Reverse(sorter)
		}

		sort.Stable(sorter)
	}

a:

	path := strings.TrimPrefix(g.URL.Path, "/")
	var components []Component
	if path == "" {
		components = []Component{{"/", "/"}}
	} else {
		parts := strings.Split(path, string([]rune{filepath.Separator}))
		components = make([]Component, len(parts)+1)
		components[0] = Component{"/", "/"}
		for i, p := range parts {
			components[i+1] = Component{p + "/", "/" + filepath.Join(parts[:i+1]...)}
		}
	}

	c.Data = &struct {
		Components []Component
		Entries    []*FileEntry
		Readme     []byte
		SortCol    string
		SortRev    bool
	}{components, entries, readme, sortCol, sortRev}

	return 200, out.HTML("index", c, "layout")
}

type FileEntry struct {
	Component
	Size       Bytes
	IsDir      bool
	IsLink     bool
	Mod        time.Time
	NumEntries int
	FileMode   os.FileMode
}

type byName []*FileEntry

func (l byName) Len() int      { return len(l) }
func (l byName) Swap(i, j int) { l[i], l[j] = l[j], l[i] }

func (l byName) Less(i, j int) bool {
	return l[i].Name < l[j].Name
}

type bySize []*FileEntry

func (l bySize) Len() int      { return len(l) }
func (l bySize) Swap(i, j int) { l[i], l[j] = l[j], l[i] }

func (l bySize) Less(i, j int) bool {
	return l[i].Size < l[j].Size
}

type byModTime []*FileEntry

func (l byModTime) Len() int      { return len(l) }
func (l byModTime) Swap(i, j int) { l[i], l[j] = l[j], l[i] }

func (l byModTime) Less(i, j int) bool {
	return l[i].Mod.Before(l[j].Mod)
}

type Component struct {
	Name string
	Path string
}

type Bytes uint64

const (
	KB = 1024 << (10 * iota)
	MB
	GB
	TB
	PB
)

func (b Bytes) String() string {
	n := 0.0
	s := ""
	switch {
	case b < 0:
		return ""
	case b < KB:
		return strconv.FormatUint(uint64(b), 10)
	case b < MB:
		s = "k"
		n = float64(b) / KB
	case b < GB:
		s = "M"
		n = float64(b) / MB
	case b < TB:
		s = "G"
		n = float64(b) / GB
	case b < PB:
		s = "T"
		n = float64(b) / TB
	default:
		s = "P"
		n = float64(b) / PB
	}

	return strconv.FormatFloat(round(n, 1), 'f', -1, 64) + s
}

// round to prec digits
func round(n float64, prec int) float64 {
	n *= float64(prec) * 10
	x := float64(int64(n + 0.5))
	return x / (float64(prec) * 10)
}

var readmePatterns = []string{
	"readme",
	"readme.md",
	"readme.mkd",
	"readme.mkdown",
	"readme.markdown",
}

func isReadme(fi os.FileInfo) bool {
	name := strings.ToLower(fi.Name())
	for _, p := range readmePatterns {
		if name == p {
			return true
		}
	}
	return false
}
