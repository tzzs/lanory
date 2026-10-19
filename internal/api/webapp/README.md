# Frontend assets embedded by the Go binary

`internal/api/static.go` embeds this directory with `go:embed`, so the release
binary serves the built React app without needing the `web/` tree on disk.

This marker file exists so the embed directive always compiles, including on a
fresh clone where the frontend has not been built yet. In that state the
embedded filesystem contains no `index.html`, and the server falls back to
`server.web_dir` (or `--web-dir`) and finally to a built-in placeholder page.

To embed the real build, copy `web/dist` here:

    make web            # builds web/dist with Vite
    cp -r web/dist/. internal/api/webapp/

`static.go` prefers an explicit `web_dir`, then the embedded copy, then the
placeholder, so a developer running `--web-dir web/dist` always gets the
freshly built assets.
