package service

import "strings"

const (
	DefaultScheduledTestPrompt = "请生成可直接运行的单文件HTML，使用内联SVG绘制鹈鹕骑自行车的二维循环动画。画面以鹈鹕和自行车为主体，展示清晰的身体结构、踩踏动作和车轮转动，配合协调的背景、配色与层次。动画应流畅自然、衔接连续，并适配不同屏幕尺寸。动画必须用 CSS @keyframes 或 SMIL（animate/animateTransform）实现，不要使用 JavaScript 或 <script> 标签。禁止依赖外部资源，只输出完整HTML，不要代码围栏或解释文字。"
	// LegacyScheduledTestPrompt is the 261 column default and the admin UI
	// pelican prompt, which omit the CSS-keyframes sentence. Plans created
	// from that form stored this exact text, so the quality evaluator must
	// treat it as the same rubric.
	LegacyScheduledTestPrompt  = "请生成可直接运行的单文件HTML，使用内联SVG绘制鹈鹕骑自行车的二维循环动画。画面以鹈鹕和自行车为主体，展示清晰的身体结构、踩踏动作和车轮转动，配合协调的背景、配色与层次。动画应流畅自然、衔接连续，并适配不同屏幕尺寸。禁止依赖外部资源，只输出完整HTML，不要代码围栏或解释文字。"
	DefaultScheduledTestCron   = "*/5 * * * *"
	maxScheduledTestPromptSize = 32 * 1024
)

func isDefaultScheduledTestPrompt(prompt string) bool {
	switch strings.TrimSpace(prompt) {
	case "", DefaultScheduledTestPrompt, LegacyScheduledTestPrompt:
		return true
	default:
		return false
	}
}
