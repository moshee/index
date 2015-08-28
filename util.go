package main

import (
	"os"
	"strings"
)

type FSStore struct{}

func (FSStore) Get(id string) string { return id }

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
	"readme.md",
	"readme.mkd",
	"readme.mkdown",
	"readme.markdown",
}

const (
	notReadme = iota
	plainReadme
	markdownReadme
)

func determineReadmeKind(fi os.FileInfo) int {
	name := strings.ToLower(fi.Name())
	if name == "readme" {
		return plainReadme
	}
	for _, p := range readmePatterns {
		if name == p {
			return markdownReadme
		}
	}
	return notReadme
}
