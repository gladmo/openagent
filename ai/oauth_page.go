package ai

// Port of pi-ai src/utils/oauth-page.ts at reference commit a276dabe:
// the browser pages shown after an OAuth redirect.

import "strings"

const logoSVG = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 800 800" aria-hidden="true"><path fill="#F09082" d="M165.29 165.29H517.36V400H400V282.65H165.29Z"/><path fill="#4D9ABF" d="M165.29 282.65H282.65V400H400V517.36H282.65V634.72H165.29Z"/><path fill="#F1BE58" d="M517.36 400H634.72V634.72H517.36Z"/></svg>`

func escapeHTML(value string) string {
	replacer := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&#39;",
	)
	return replacer.Replace(value)
}

func renderOauthPage(title, heading, message, details string) string {
	escapedDetails := ""
	if details != "" {
		escapedDetails = `<div class="details">` + escapeHTML(details) + `</div>`
	}
	var builder strings.Builder
	builder.WriteString(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>`)
	builder.WriteString(escapeHTML(title))
	builder.WriteString(`</title>
  <style>
    :root {
      --text: #fafafa;
      --text-dim: #a1a1aa;
      --page-bg: #09090b;
      --font-sans: ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", Arial, "Noto Sans", sans-serif, "Apple Color Emoji", "Segoe UI Emoji", "Segoe UI Symbol", "Noto Color Emoji";
      --font-mono: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", "Courier New", monospace;
    }
    * { box-sizing: border-box; }
    html { color-scheme: dark; }
    body {
      margin: 0;
      min-height: 100vh;
      display: flex;
      align-items: center;
      justify-content: center;
      padding: 24px;
      background: var(--page-bg);
      color: var(--text);
      font-family: var(--font-sans);
      text-align: center;
    }
    main {
      width: 100%;
      max-width: 560px;
      display: flex;
      flex-direction: column;
      align-items: center;
      justify-content: center;
    }
    .logo {
      width: 72px;
      height: 72px;
      display: block;
      margin-bottom: 24px;
    }
    h1 {
      margin: 0 0 10px;
      font-size: 28px;
      line-height: 1.15;
      font-weight: 650;
      color: var(--text);
    }
    p {
      margin: 0;
      line-height: 1.7;
      color: var(--text-dim);
      font-size: 15px;
    }
    .details {
      margin-top: 16px;
      font-family: var(--font-mono);
      font-size: 13px;
      color: var(--text-dim);
      white-space: pre-wrap;
      word-break: break-word;
    }
  </style>
</head>
<body>
  <main>
    <div class="logo">`)
	builder.WriteString(logoSVG)
	builder.WriteString(`</div>
    <h1>`)
	builder.WriteString(escapeHTML(heading))
	builder.WriteString(`</h1>
    <p>`)
	builder.WriteString(escapeHTML(message))
	builder.WriteString(`</p>
    `)
	builder.WriteString(escapedDetails)
	builder.WriteString(`
  </main>
</body>
</html>`)
	return builder.String()
}

// OauthSuccessHTML renders the success page shown after a completed
// OAuth flow.
func OauthSuccessHTML(message string) string {
	return renderOauthPage("Authentication successful", "Authentication successful", message, "")
}

// OauthErrorHTML renders the failure page shown after a rejected OAuth
// flow.
func OauthErrorHTML(message, details string) string {
	return renderOauthPage("Authentication failed", "Authentication failed", message, details)
}
