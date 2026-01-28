package Ext

import "net/http"

type Tokens struct {
	PageToken string
	CsrfToken string
	Cookies   []*http.Cookie
}
