package main

import (
	"image/jpeg"
	"io/ioutil"
	"log"
	"math"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"golang.org/x/image/draw"

	"ktkr.us/pkg/airlift/thumb"
	"ktkr.us/pkg/fmtutil"
	"ktkr.us/pkg/gas"
	"ktkr.us/pkg/gas/out"
)

const (
	thumbWidth  = 150
	thumbHeight = 100
)

var Conf struct {
	Root            string `default:"."`
	ThumbDir        string
	ThumbEnable     bool `default:"true"`
	GalleryImages   int  `default:"25"`
	ZipFolderEnable bool `default:"false"`
}

var cache *thumb.Cache

func main() {
	err := gas.EnvConf(&Conf, "INDEX_")
	if err != nil {
		log.Fatal(err)
	}

	u, err := user.Current()
	if err != nil {
		log.Fatal(err)
	}

	if Conf.ThumbDir == "" {
		Conf.ThumbDir = filepath.Join(u.HomeDir, ".thumbs")
	}

	enc := thumb.JPEGEncoder{&jpeg.Options{90}}
	cache, err = thumb.NewCache(Conf.ThumbDir, enc, FSStore{}, draw.ApproxBiLinear)
	if err != nil {
		log.Fatal(err)
	}
	go cache.Serve()

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

type FileEntry struct {
	Component
	Size       fmtutil.SI
	IsDir      bool
	IsLink     bool
	Mod        time.Time
	NumEntries int
	FileMode   os.FileMode
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

	var form struct {
		SortCol     string `form:"s"`
		SortRev     bool   `form:"r"`
		GalleryPage int    `form:"p"`
		Thumb       bool   `form:"t"`
	}

	g.UnmarshalForm(&form)

	if !fi.IsDir() {
		if Conf.ThumbEnable && form.Thumb && thumb.FormatSupported(filepath.Ext(p)) {
			thumbPath := cache.Get(p, thumbWidth, thumbHeight)
			// serve original image if we can't thumbnail
			if thumbPath != "" {
				http.ServeFile(g, g.Request, thumbPath)
				return g.Stop()
			}
		}
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
		readmeKind    int
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

		// only pick first one encountered
		if readmeKind == notReadme {
			if readmeKind = determineReadmeKind(fi); readmeKind != notReadme {
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
		if thumb.FormatSupported(filepath.Ext(fi.Name())) {
			imageFiles = append(imageFiles, e)
		} else {
			nonImageFiles = append(nonImageFiles, e)
		}
	}

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
		PlainReadme  bool
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
		readmeKind == plainReadme,
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
