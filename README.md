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

Adding the following to your `nginx.conf` can make the indexer accessible from `files.example.com`:

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

### Environment

Name | Default | Description
-----|---------|-------------
INDEX_ROOT | "." | The root directory from which to start serving file listings.
INDEX_GALLERY_IMAGES | 25 | The maximum number of images per gallery page.
