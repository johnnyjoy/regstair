package admin

import (
	"crypto/sha256"
	"embed"
	"fmt"
	"html/template"
)

//go:embed templates/*.html static/*
var adminAssets embed.FS

var dashboardTemplate = template.Must(template.ParseFS(adminAssets, "templates/dashboard.html"))
var loginTemplate = template.Must(template.ParseFS(adminAssets, "templates/login.html"))
var setupTemplate = template.Must(template.ParseFS(adminAssets, "templates/setup.html"))

var adminStylesheetURL = versionedAssetURL("static/admin.css")
var adminScriptURL = versionedAssetURL("static/admin.js")

func versionedAssetURL(name string) string {
	contents := mustReadAdminAsset(name)
	digest := sha256.Sum256(contents)
	return fmt.Sprintf("/admin/%s?v=%x", name, digest[:8])
}

func mustReadAdminAsset(name string) []byte {
	contents, err := adminAssets.ReadFile(name)
	if err != nil {
		panic(err)
	}
	return contents
}
