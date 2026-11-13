package oauth

import (
	"encoding/json"
	"fmt"
	"html"
	"strings"
)

// renderLoginPage builds the provider picker. It is server-rendered so an MCP
// client, which has no frontend of its own, can complete the flow too.
func renderLoginPage(state string, providers []string, emailEnabled bool) string {
	var buttons []string
	for _, name := range providers {
		if name == "email" {
			continue
		}
		label, ok := providerLabels[name]
		if !ok {
			label = name
		}
		buttons = append(buttons, fmt.Sprintf(
			`<a class="btn" href="/login/start?provider=%s&state=%s">使用 %s 登录</a>`,
			html.EscapeString(name), html.EscapeString(state), html.EscapeString(label)))
	}
	if emailEnabled {
		buttons = append(buttons, fmt.Sprintf(
			`<a class="btn secondary" href="/login/email?state=%s">使用邮箱登录</a>`,
			html.EscapeString(state)))
	}
	// The browser remembers which button was clicked. Redirect before the page
	// paints, so the user never sees the picker when they already chose. The
	// state is passed as JSON so it cannot break out of the script.
	stateJSON, _ := json.Marshal(state)
	script := `<script>
  var pick = sessionStorage.getItem('diary.provider');
  if (pick) { window.location.replace('/login/start?provider=' + encodeURIComponent(pick) + '&state=' + encodeURIComponent(` + string(stateJSON) + `)); }
</script>`
	return page("登录 mcp-diary", script+`<h1>登录 mcp-diary</h1><div class="buttons">`+
		strings.Join(buttons, "")+`</div>`)
}

var providerLabels = map[string]string{
	"google": "Google",
	"gitee":  "Gitee",
}

func renderEmailPage(state, message string) string {
	notice := ""
	if message != "" {
		notice = `<p class="error">` + html.EscapeString(message) + `</p>`
	}
	escaped := html.EscapeString(state)
	body := `<h1>邮箱登录</h1>` + notice + `
<form method="post" action="/login/email">
  <input type="hidden" name="state" value="` + escaped + `">
  <label>邮箱<input type="email" name="email" required autocomplete="username"></label>
  <label>密码<input type="password" name="password" required autocomplete="current-password"></label>
  <button type="submit">登录</button>
</form>
<p class="links"><a href="/login/forgot">忘记密码</a> · <a href="#" onclick="document.getElementById('register').hidden=false;return false">注册新账号</a></p>
<div id="register" hidden>
  <h1>注册</h1>
  <form id="reg">
    <input type="hidden" name="state" value="` + escaped + `">
    <label>邮箱<input type="email" name="email" required></label>
    <label>验证码<input type="text" name="code" inputmode="numeric" autocomplete="one-time-code" required></label>
    <label>密码<input type="password" name="password" required minlength="8" placeholder="至少 8 位，含字母和数字"></label>
    <label>显示名（可选）<input type="text" name="display_name" maxlength="80"></label>
    <button type="button" id="send" class="secondary">获取验证码</button>
    <button type="submit">注册</button>
  </form>
  <p id="reg-msg"></p>
</div>
<script>
const regMsg = document.getElementById('reg-msg');
document.getElementById('send').addEventListener('click', async () => {
  const email = document.querySelector('#reg [name=email]').value;
  const resp = await fetch('/auth/email/code', {method:'POST', headers:{'Content-Type':'application/json'},
    body: JSON.stringify({email, purpose:'register'})});
  const data = await resp.json();
  regMsg.textContent = resp.ok ? '验证码已发送，10 分钟内有效' : (data.error && data.error.message || '发送失败');
});
document.getElementById('reg').addEventListener('submit', async (e) => {
  e.preventDefault();
  const data = Object.fromEntries(new FormData(e.target));
  const resp = await fetch('/auth/email/register', {method:'POST', headers:{'Content-Type':'application/json'},
    body: JSON.stringify(data)});
  const body = await resp.json();
  if (resp.ok && body.redirect_url) { window.location.assign(body.redirect_url); return; }
  regMsg.textContent = (body.error && body.error.message) || '注册失败';
});
</script>`
	return page("邮箱登录", body)
}

func renderForgotPage() string {
	body := `<h1>重置密码</h1>
<form id="reset">
  <label>邮箱<input type="email" name="email" required></label>
  <label>验证码<input type="text" name="code" inputmode="numeric" required></label>
  <label>新密码<input type="password" name="new_password" required minlength="8"></label>
  <button type="submit">重置密码</button>
</form>
<p id="msg"></p>
<script>
document.getElementById('reset').addEventListener('submit', async (e) => {
  e.preventDefault();
  const data = Object.fromEntries(new FormData(e.target));
  const msg = document.getElementById('msg');
  const code = await fetch('/auth/email/code', {method:'POST', headers:{'Content-Type':'application/json'},
    body: JSON.stringify({email: data.email, purpose: 'reset'})});
  const reset = await fetch('/auth/email/reset', {method:'POST', headers:{'Content-Type':'application/json'},
    body: JSON.stringify(data)});
  msg.textContent = reset.ok ? '密码已重置，请返回登录' : '重置失败，请检查验证码';
});
</script>`
	return page("重置密码", body)
}

func page(title, body string) string {
	return `<!doctype html>
<html lang="zh">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>` + html.EscapeString(title) + `</title>
<style>
  body { font: 16px/1.5 -apple-system, "Segoe UI", sans-serif; background:#f6f7f9; margin:0; }
  main { max-width: 380px; margin: 8vh auto; background:#fff; padding: 32px; border-radius: 12px;
         box-shadow: 0 1px 3px rgba(0,0,0,.08); }
  h1 { font-size: 20px; margin: 0 0 24px; }
  .buttons { display:flex; flex-direction:column; gap:12px; }
  a.btn, button { display:block; text-align:center; background:#1a73e8; color:#fff; text-decoration:none;
         padding:10px 16px; border-radius:8px; border:0; font-size:16px; cursor:pointer; width:100%; }
  button.secondary { background:#fff; color:#1a73e8; border:1px solid #dadce0; margin-bottom:12px; }
  a.secondary { background:#fff; color:#1a73e8; border:1px solid #dadce0; }
  label { display:block; margin-bottom:14px; font-size:14px; color:#444; }
  input { display:block; width:100%; box-sizing:border-box; margin-top:4px; padding:8px 10px;
          border:1px solid #dadce0; border-radius:6px; font-size:16px; }
  .error { color:#d93025; background:#fce8e6; padding:8px 12px; border-radius:6px; }
  .links { text-align:center; margin-top:16px; font-size:14px; }
</style>
</head>
<body><main>` + body + `</main></body></html>`
}
