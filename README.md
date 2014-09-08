## Index

This is a directory listing web server. It's a refresh of my old httplistd project, which I made when I didn't know much about Go and stuff.

### Install and run

```
$ go get github.com/moshee/index
$ cd $GOPATH/src/github.com/moshee/index
$ go build
$ INDEX_ROOT=~/files GAS_PORT=8888 ./index
$ open http://localhost:8888
```

### Environment

Name | Default | Description
-----|---------|-------------
INDEX_ROOT | "." | The root directory from which to start serving file listings.
