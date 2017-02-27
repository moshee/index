## Index

This is a directory listing web server. It's a refresh of my old httplistd
project, which I made when I didn't know much about Go and stuff.

### Install and run

```
$ go get github.com/moshee/index
$ cd $GOPATH/src/github.com/moshee/index
$ go build
$ INDEX_ROOT=~/files GAS_PORT=8888 ./index
$ open http://localhost:8888
```

Adding the following to your `nginx.conf` can make the indexer accessible from
`files.example.com`:

```nginx
server {
    listen 80;
    server_name files.example.com;
    location / {
        proxy_pass http://localhost:8888;
        proxy_set_header Host $http_host;
    }
}
```

#### Building

Requires Go 1.7.

To generate support files for binary-packaged static assets, run `go generate`
before `go build`.

### Environment

Name                              | Default       | Description
----------------------------------|---------------|--------------
INDEX_ROOT                        | `"."`         | The root directory from which to start serving file listings.
INDEX_THUMB_DIR                   | `"~/.thumbs"` | The directory to cache thumbnails in if `INDEX_THUMB_ENABLE=1`.
INDEX_THUMB_ENABLE                | true          | Enable generating and caching thumbnails of gallery images.
INDEX_GALLERY_IMAGES              | 25            | The maximum number of images per gallery page.
INDEX_ZIP_FOLDER_ENABLE           | false         | Enable downloading all files in current directory as a zip file.
INDEX_ZIP_FOLDER_ENABLE_RECURSIVE | false         | Enable downloading entire current tree recursively as a zip file.
INDEX_ZIP_FOLDER_MAX_CONCURRENCY  | 0             | Limit global number of concurrent zippers. 0 applies no limit. Must be ≥0.
INDEX_FILE_LIST_SHOW_MODES        | true          | Enable file modes (`drwxrwxrwx`) column in file list.
INDEX_RESOURCE_DIR                | `""`          | Directory in which to load resources (static files and templates). Uses files packed in binary if empty.
