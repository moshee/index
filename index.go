package main

import (
	"archive/zip"
	"image/jpeg"
	"io"
	"io/ioutil"
	"log"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"go4.org/syncutil"

	"golang.org/x/image/draw"

	"ktkr.us/pkg/airlift/contentdisposition"
	"ktkr.us/pkg/airlift/thumb"
	"ktkr.us/pkg/fmtutil"
	"ktkr.us/pkg/gas"
	"ktkr.us/pkg/gas/out"
	"ktkr.us/pkg/vfs"
	"ktkr.us/pkg/vfs/bindata"
)

//go:generate bindata -skip=*.sw[nop] static templates

const (
	thumbWidth  = 150
	thumbHeight = 100
)

var Conf struct {
	Root                     string `default:"."`
	ThumbDir                 string
	ThumbEnable              bool   `default:"true"`
	GalleryImages            int    `default:"25"`
	ZipFolderEnable          bool   `default:"false"` // enable download directory as zip
	ZipFolderEnableRecursive bool   `default:"false"` // enable download directory recursively as zip
	ZipFolderMaxConcurrency  int    `default:"0"`     // absolutely limit global number of concurrent zippers
	FileListShowModes        bool   `default:"true"`  // show file modes (i.e. drwxrwxrwx)
	ResourceDir              string // location of static assets on disk
}

var (
	cache *thumb.Cache
	gate  *syncutil.Gate
)

func init() {
	t, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		log.Fatal("not ok")
	}
	t.DialContext = (&net.Dialer{Timeout: 60 * time.Second}).DialContext
	t.IdleConnTimeout = 30 * time.Minute
}

func main() {
	err := gas.EnvConf(&Conf, "INDEX_")
	if err != nil {
		log.Fatal(err)
	}

	var (
		r  = gas.New()
		fs vfs.FileSystem
	)

	if Conf.ResourceDir != "" {
		log.Print("using disk filesystem")
		nfs, err := vfs.Native(Conf.ResourceDir)
		if err != nil {
			log.Fatal(err)
		}
		fs = vfs.Fallback(nfs, bindata.Root)
		r.StaticHandler("/static", fs)
	} else {
		log.Print("using binary filesystem")
		fs = bindata.Root
		r.StaticHandler("/static", vfs.Subdir(fs, "static"))
	}

	out.TemplateFS(fs)

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

	if Conf.ZipFolderMaxConcurrency > 0 {
		gate = syncutil.NewGate(Conf.ZipFolderMaxConcurrency)
	}

	r.Get("{path}", getIndex)
	log.Fatal(r.Ignition())
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
		newpath := strings.TrimSuffix(g.URL.Path, "/")
		if g.URL.RawQuery != "" {
			newpath += "?" + g.URL.RawQuery
		}
		return 303, out.Redirect(newpath)
	}

	var form struct {
		Zip         bool   `form:"zip"`
		Recursive   bool   `form:"rec"`
		SortCol     string `form:"s"`
		SortRev     bool   `form:"r"`
		GalleryPage int    `form:"p"`
		Thumb       bool   `form:"t"`
	}
	g.UnmarshalForm(&form)

	dir := http.Dir(Conf.Root)
	f, err := dir.Open(g.URL.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return 404, out.HTML("404", err, "layout")
		} else {
			return 500, out.HTML("500", err, "layout")
		}
	}

	fi, err := f.Stat()
	if err != nil {
		return 500, out.HTML("500", err, "layout")
	}

	if fi.IsDir() && form.Zip && Conf.ZipFolderEnable {
		var (
			fhs []*zip.FileHeader
			err error
		)

		if form.Recursive && Conf.ZipFolderEnableRecursive {
			fhs, err = walk(g.URL.Path)
		} else {
			fhs, err = readdirnames(g.URL.Path)
		}

		if err != nil {
			return 500, out.HTML("500", err, "layout")
		}

		return 200, &zipper{g.URL.Path, fhs}
	}

	base := strings.ToLower(filepath.Base(g.URL.Path))
	if base == "index.html" || base == "index.htm" {
		http.ServeContent(g, g.Request, base, fi.ModTime(), f)
		return g.Stop()
	}

	if !fi.IsDir() {
		// file was requested
		if Conf.ThumbEnable && form.Thumb && thumb.FormatSupported(filepath.Ext(fi.Name())) {
			p := filepath.Join(Conf.Root, g.URL.Path)
			thumbPath := cache.Get(p, thumbWidth, thumbHeight)
			// serve original image if we can't thumbnail
			if thumbPath != "" {
				http.ServeFile(g, g.Request, thumbPath)
				return g.Stop()
			}
		}
		http.ServeContent(g, g.Request, fi.Name(), fi.ModTime(), f)
		return g.Stop()
	}

	// directory listing requested

	fis, err := f.Readdir(-1)
	if err != nil {
		return 500, out.HTML("500", err, "layout")
	}

	var (
		entries       = make([]*FileEntry, 0, len(fis))
		readme        []byte
		readmeKind    int
		imageFiles    []*FileEntry
		nonImageFiles []*FileEntry
	)

	for _, fi := range fis {
		if strings.HasPrefix(fi.Name(), ".") {
			continue
		}

		var (
			path        = filepath.Join(g.URL.Path, fi.Name())
			isLink      = fi.Mode()&os.ModeSymlink != 0
			currentFile http.File
		)

		if isLink {
			currentFile, err = dir.Open(path)
			if err != nil {
				return 500, out.HTML("500", err, "layout")
			}
			fi, err = currentFile.Stat()
			if err != nil {
				return 500, out.HTML("500", err, "layout")
			}
		}

		// only pick first one encountered
		if readmeKind == notReadme {
			if readmeKind = determineReadmeKind(fi); readmeKind != notReadme {
				f, err := dir.Open(path)
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
				Path: (&url.URL{Path: filepath.Join(g.URL.Path, fi.Name())}).String(),
			},
			Size:     fmtutil.SI(fi.Size()),
			IsDir:    fi.IsDir(),
			IsLink:   isLink,
			Mod:      fi.ModTime(),
			FileMode: fi.Mode(),
		}

		if fi.IsDir() {
			if currentFile == nil {
				currentFile, err = dir.Open(path)
				if err != nil {
					return 500, out.HTML("500", err, "layout")
				}
			}
			fis, err = currentFile.Readdir(-1)
			if err != nil {
				log.Print(err)
			} else {
				for _, contained := range fis {
					if !strings.HasPrefix(contained.Name(), ".") {
						e.NumEntries++
					}
				}
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

	var (
		path         = strings.TrimPrefix(g.URL.Path, "/")
		components   []Component
		showGallery  = len(imageFiles) > len(entries)/2
		galleryPages int
	)

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

	data := &struct {
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
		Config       interface{}
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
		&Conf,
	}

	return 200, out.HTML("index", data, "layout")
}

func readdirnames(root string) ([]*zip.FileHeader, error) {
	f, err := http.Dir(Conf.Root).Open(root)
	if err != nil {
		return nil, err
	}

	names, err := f.Readdir(0)
	if err != nil {
		return nil, err
	}

	var (
		fhs  = make([]*zip.FileHeader, 0, len(names))
		base = filepath.Base(root)
	)

	for _, fi := range names {
		if !fi.Mode().IsRegular() {
			continue
		}
		fh, err := zip.FileInfoHeader(fi)
		if err != nil {
			return nil, err
		}
		name := filepath.Base(fi.Name())
		fh.Name = strings.TrimPrefix(filepath.Join(base, name), string([]rune{filepath.Separator}))
		fhs = append(fhs, fh)
	}

	return fhs, nil
}

func walk(root string) ([]*zip.FileHeader, error) {
	fhs := make([]*zip.FileHeader, 0)
	dir := filepath.Dir(root)

	err := filepath.Walk(root, func(path string, fi os.FileInfo, err error) error {
		if !fi.Mode().IsRegular() {
			return nil
		}
		if err != nil {
			return err
		}
		fh, err := zip.FileInfoHeader(fi)
		if err != nil {
			return err
		}
		fh.Name, _ = filepath.Rel(dir, path)
		fhs = append(fhs, fh)
		return nil
	})

	if err != nil {
		return nil, err
	}
	return fhs, nil
}

type zipper struct {
	dir string
	fhs []*zip.FileHeader
}

func (z *zipper) Output(code int, g *gas.Gas) {
	if gate != nil {
		gate.Start()
		defer gate.Done()
	}

	dir := filepath.Dir(z.dir)

	names := make([]string, len(z.fhs))
	for i, fh := range z.fhs {
		names[i] = fh.Name
	}

	g.Header().Set("Content-Type", "application/zip")
	contentdisposition.SetFilename(g, filepath.Base(z.dir)+".zip")
	g.WriteHeader(code)

	zw := zip.NewWriter(g)
	defer zw.Close()
	for _, fh := range z.fhs {
		path := filepath.Join(Conf.Root, dir, fh.Name)
		f, err := os.Open(path)
		if err != nil {
			log.Printf("zipper: %v", err)
			continue
		}
		// UTF-8 filename mode (see Appendix D of ZIP spec)
		fh.Flags |= (1 << 11)
		fh.Method = zip.Deflate
		w, err := zw.CreateHeader(fh)
		if err != nil {
			log.Printf("zipper: %v", err)
			continue
		}
		_, err = io.Copy(w, f)
		if err != nil {
			log.Printf("zipper: %v", err)
			break
		}
		f.Close()
	}
}
