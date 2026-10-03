package edge

import (
	"fmt"
	"html"
	"net/http"
	"strings"
	"unicode/utf8"

	"nyatunnel-server/internal/server/store"
)

// report serves /__nyatunnel/report on every tunnel host: visitors can flag a tunnel (phishing,
// illegal content…) without knowing anything about NyaTunnel. Reports go to the audit log and the
// administrators' notification channels.
func (g *gate) report(w http.ResponseWriter, req *http.Request, r *route, clientIP string) {
	if req.Method == http.MethodGet {
		page(w, http.StatusOK, "举报此页面", fmt.Sprintf(`<h2>举报此页面</h2><p class="hint">%s</p>
<form method="post" action="%sreport" style="text-align:left">
<p><label>原因<br><select name="kind" style="font:inherit;padding:.4rem;width:100%%">
<option value="phishing">冒充其他网站 / 钓鱼</option><option value="malware">恶意软件</option>
<option value="illegal">违法或不良内容</option><option value="other">其他</option></select></label></p>
<p><label>说明（可选）<br><textarea name="detail" rows="4" maxlength="1000" style="font:inherit;width:100%%;box-sizing:border-box"></textarea></label></p>
<p><label>联系方式（可选）<br><input name="contact" maxlength="200" style="width:100%%;box-sizing:border-box"></label></p>
<p style="text-align:center"><button>提交举报</button></p></form>`, html.EscapeString(r.Host), internalPrefix))
		return
	}
	if req.Method != http.MethodPost {
		http.NotFound(w, req)
		return
	}
	key := "report|" + clientIP
	if g.blocked(key) {
		page(w, http.StatusTooManyRequests, "举报过于频繁", `<h2>举报过于频繁</h2><p class="hint">请稍后再试。</p>`)
		return
	}
	g.fail(key) // every report counts towards the limit
	req.Body = http.MaxBytesReader(w, req.Body, 8192)
	_ = req.ParseForm()
	kind := req.PostForm.Get("kind")
	switch kind {
	case "phishing", "malware", "illegal", "other":
	default:
		kind = "other"
	}
	clip := func(s string, n int) string {
		s = strings.TrimSpace(s)
		if utf8.RuneCountInString(s) > n {
			s = string([]rune(s)[:n])
		}
		return s
	}
	detail := clip(req.PostForm.Get("detail"), 1000)
	contact := clip(req.PostForm.Get("contact"), 200)
	text := fmt.Sprintf("隧道 %s（%s）被举报：%s。说明：%s。联系方式：%s。来源 %s", r.Name, r.Host, kind, detail, contact, clientIP)
	g.e.opt.Audit(req.Context(), store.AuditEvent{ActorType: "anonymous", Action: "tunnel.reported", Target: r.TunnelID, Detail: text, IP: clientIP})
	g.e.opt.Notify("abuse.report", "收到举报："+r.Host, text)
	page(w, http.StatusOK, "已收到举报", `<h2>已收到举报</h2><p class="hint">感谢你的反馈，管理员会尽快处理。</p>`)
}
