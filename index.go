package main

import (
	"io/ioutil"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"ktkr.us/pkg/fmtutil"
	"ktkr.us/pkg/gas"
	"ktkr.us/pkg/gas/out"
)

var Conf struct {
	Root          string `default:"."`
	GalleryImages int    `default:"25"`
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
	if g.URL.Path != "/" && strings.HasSuffix(g.URL.Path, "/") {
		return 303, out.Redirect(strings.TrimSuffix(g.URL.Path, "/"))
	}
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

	base := strings.ToLower(filepath.Base(g.URL.Path))
	if base == "index.html" || base == "index.htm" {
		f, err := os.Open(p)
		if err != nil {
			c.Data = err
			return 500, out.HTML("500", c, "layout")
		}
		http.ServeContent(g, g.Request, base, fi.ModTime(), f)
		return g.Stop()
	}

	if !fi.IsDir() {
		http.ServeFile(g, g.Request, p)
		return g.Stop()
	}

	fis, err := ioutil.ReadDir(p)
	if err != nil {
		c.Data = err
		return 500, out.HTML("500", c, "layout")
	}

	entries := make([]*FileEntry, 0, len(fis))
	var (
		readme        []byte
		imageFiles    []*FileEntry
		nonImageFiles []*FileEntry
	)

	for _, fi := range fis {
		if strings.HasPrefix(fi.Name(), ".") {
			continue
		}
		path := filepath.Join(p, fi.Name())

		isLink := fi.Mode()&os.ModeSymlink != 0
		if isLink {
			fi, err = os.Stat(path)
			if err != nil {
				c.Data = err
				return 500, out.HTML("500", c, "layout")
			}
		}

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
			Size:     fmtutil.SI(fi.Size()),
			IsDir:    fi.IsDir(),
			IsLink:   isLink,
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
		if isImage(fi.Name()) {
			imageFiles = append(imageFiles, e)
		} else {
			nonImageFiles = append(nonImageFiles, e)
		}
	}

	var form struct {
		SortCol     string `form:"s"`
		SortRev     bool   `form:"r"`
		GalleryPage int    `form:"p"`
	}

	log.Print(g.UnmarshalForm(&form))

	log.Print(form)

	if form.SortCol != "" {
		var sorter sort.Interface
		s := true

		switch form.SortCol {
		case "n":
			sorter = byName(entries)
		case "s":
			sorter = bySize(entries)
		case "m":
			sorter = byModTime(entries)
		default:
			s = false
		}

		if s {
			if form.SortRev {
				sorter = sort.Reverse(sorter)
			}

			sort.Stable(sorter)
		}
	}

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

	numEntries := len(entries)
	showGallery := len(imageFiles) > len(entries)/2
	var galleryPages int
	if showGallery {
		entries = nonImageFiles
		galleryPages = int(math.Ceil(float64(len(imageFiles)) / float64(Conf.GalleryImages)))

		if form.GalleryPage < 1 {
			form.GalleryPage = 1
		}
		off := (form.GalleryPage - 1) * Conf.GalleryImages
		if off < len(imageFiles) {
			if len(imageFiles)-off < Conf.GalleryImages {
				imageFiles = imageFiles[off:]
			} else {
				imageFiles = imageFiles[off : off+Conf.GalleryImages]
			}
		}
	}

	c.Data = &struct {
		Components   []Component
		Entries      []*FileEntry
		ImageFiles   []*FileEntry
		Readme       []byte
		SortCol      string
		SortRev      bool
		Gallery      bool
		GalleryPage  int
		NextPage     int
		PrevPage     int
		GalleryPages int
		NumEntries   int
	}{
		components,
		entries,
		imageFiles,
		readme,
		form.SortCol,
		form.SortRev,
		showGallery,
		form.GalleryPage,
		form.GalleryPage + 1,
		form.GalleryPage - 1,
		galleryPages,
		numEntries,
	}

	return 200, out.HTML("index", c, "layout")
}

type FileEntry struct {
	Component
	Size       fmtutil.SI
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

var imageExtensions = []string{
	".jpg",
	".jpeg",
	".png",
	".gif",
	".webp",
	".tif",
	".tiff",
	".bmp",
	".svg",
}

func isImage(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	for _, e := range imageExtensions {
		if e == ext {
			return true
		}
	}
	return false
}
