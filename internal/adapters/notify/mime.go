package notify

import "mime"

func mimeWord(s string) string { return mime.QEncoding.Encode("utf-8", s) }
