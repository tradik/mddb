package main

import (
	"html/template"
	"net/http"
)

// The consent page of the MCP authorization server (#322). The person
// authorizing a client proves who they are with an MCP API key — mddb has no
// user accounts to log into — and the client gets tokens that stand for it.
// Colours meet WCAG 2.2 AA contrast; the page may not be framed. The CSP
// has no form-action: browsers apply it to the redirect after the form, and
// that redirect goes to the client's callback on another origin.

var oauthConsentPage = template.Must(template.New("consent").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<meta name="robots" content="noindex,nofollow">
<meta name="description" content="Authorize an application to use this mddb MCP server">
<title>Authorize · mddb</title>
<style>
:root{--bg:#f8f9fa;--card:#fff;--ink:#202124;--muted:#5f6368;--accent:#1a73e8;--err:#c5221f;--line:#dadce0}
@media (prefers-color-scheme:dark){:root{--bg:#202124;--card:#292a2d;--ink:#e8eaed;--muted:#bdc1c6;--accent:#8ab4f8;--err:#f28b82;--line:#5f6368}}
body{margin:0;background:var(--bg);color:var(--ink);font:16px/1.5 "Segoe UI",Roboto,"Helvetica Neue",sans-serif}
main{max-width:28rem;margin:10vh auto;padding:2rem 1.5rem;background:var(--card);border:1px solid var(--line);border-radius:12px}
h1{font-size:1.25rem;margin:0 0 .5rem}p{color:var(--muted);margin:.25rem 0 1rem}
label{display:block;font-weight:600;margin-bottom:.25rem}
input[type=password]{box-sizing:border-box;width:100%;padding:.6rem;border:1px solid var(--line);border-radius:6px;background:var(--bg);color:var(--ink);font:inherit}
.row{display:flex;gap:.75rem;margin-top:1.25rem}
button{flex:1;padding:.6rem;border-radius:6px;font:inherit;font-weight:600;cursor:pointer;border:1px solid var(--accent)}
.allow{background:var(--accent);color:var(--card)}.deny{background:transparent;color:var(--accent)}
.err{color:var(--err);font-weight:600}code{word-break:break-all}
</style></head><body><main>
{{if .Fatal}}<h1>Authorization failed</h1><p class="err" role="alert">{{.Fatal}}</p>{{else}}
<h1>Connect {{if .Req.ClientName}}{{.Req.ClientName}}{{else}}an application{{end}} to mddb</h1>
<p>It will be able to do what the MCP API key you enter can do, until that key is deleted. Tokens are sent back to <code>{{.Req.RedirectURI}}</code>.</p>
{{if .Error}}<p class="err" role="alert">{{.Error}}</p>{{end}}
<form method="post" action="/oauth/authorize">
<input type="hidden" name="response_type" value="code">
<input type="hidden" name="client_id" value="{{.Req.ClientID}}">
<input type="hidden" name="redirect_uri" value="{{.Req.RedirectURI}}">
<input type="hidden" name="state" value="{{.Req.State}}">
<input type="hidden" name="code_challenge" value="{{.Req.Challenge}}">
<input type="hidden" name="code_challenge_method" value="{{.Req.Method}}">
<input type="hidden" name="scope" value="{{.Req.Scope}}">
<input type="hidden" name="resource" value="{{.Req.Resource}}">
<label for="api_key">MCP API key</label>
<input id="api_key" name="api_key" type="password" autocomplete="off" autofocus>
<div class="row"><button class="deny" name="decision" value="deny">Deny</button>
<button class="allow" name="decision" value="allow">Authorize</button></div>
</form>{{end}}
</main></body></html>`))

type oauthPageData struct {
	Req          authzRequest
	Error, Fatal string
}

func renderOAuthPage(w http.ResponseWriter, status int, d oauthPageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; frame-ancestors 'none'")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.WriteHeader(status)
	_ = oauthConsentPage.Execute(w, d)
}

func renderOAuthConsent(w http.ResponseWriter, status int, a authzRequest, errMsg string) {
	renderOAuthPage(w, status, oauthPageData{Req: a, Error: errMsg})
}

func renderOAuthError(w http.ResponseWriter, msg string) {
	renderOAuthPage(w, http.StatusBadRequest, oauthPageData{Fatal: msg})
}
