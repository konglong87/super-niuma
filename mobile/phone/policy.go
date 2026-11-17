package phone

import (
	"strings"
	"unicode"
)

// Risk names why a step needs the person. Empty means no risk was detected.
type Risk string

const (
	RiskNone     Risk = ""
	RiskSend     Risk = "send"
	RiskPay      Risk = "pay"
	RiskDelete   Risk = "delete"
	RiskInstall  Risk = "install"
	RiskCall     Risk = "call"
	RiskPublish  Risk = "publish"
	RiskTransfer Risk = "transfer"
	RiskSubmit   Risk = "submit"
)

// riskWords maps what a control says to the risk of pressing it. English
// entries match whole words; entries with Han characters match anywhere.
// The list is deliberately short and literal: a missed word means a step
// runs without asking, so add words when a real app shows one.
var riskWords = []struct {
	risk  Risk
	words []string
}{
	{RiskPay, []string{"pay", "payment", "buy", "purchase", "checkout", "place order", "支付", "付款", "购买", "下单", "提交订单", "结算", "立即付款"}},
	{RiskTransfer, []string{"transfer", "send money", "转账", "汇款"}},
	{RiskSend, []string{"send", "发送"}},
	{RiskDelete, []string{"delete", "remove", "erase", "删除", "移除", "清空"}},
	{RiskInstall, []string{"install", "uninstall", "安装", "卸载"}},
	{RiskCall, []string{"call", "dial", "拨打", "呼叫"}},
	{RiskPublish, []string{"post", "publish", "share", "发布", "分享"}},
	{RiskSubmit, []string{"submit", "提交"}},
}

// messagingApps are apps where pressing enter in a text field sends it.
var messagingApps = map[string]bool{
	"com.google.android.apps.messaging": true,
	"com.android.mms":                   true,
	"com.android.messaging":             true,
	"com.samsung.android.messaging":     true,
	"com.tencent.mm":                    true,
	"com.tencent.mobileqq":              true,
	"com.whatsapp":                      true,
	"org.telegram.messenger":            true,
	"com.alibaba.android.rimet":         true,
	"com.ss.android.lark":               true,
}

// LabelRisk classifies a control by its label.
func LabelRisk(label string) Risk {
	lower := strings.ToLower(label)
	words := strings.FieldsFunc(lower, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	joined := " " + strings.Join(words, " ") + " "
	for _, group := range riskWords {
		for _, w := range group.words {
			if hasHan(w) {
				if strings.Contains(lower, w) {
					return group.risk
				}
			} else if strings.Contains(joined, " "+w+" ") {
				return group.risk
			}
		}
	}
	return RiskNone
}

func hasHan(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}

// SubmitRisk is the risk of pressing the keyboard's action key in pkg.
func SubmitRisk(pkg string) Risk {
	if messagingApps[pkg] {
		return RiskSend
	}
	return RiskNone
}

// Describe is a plain-language name for the risk, for the approval card.
func (r Risk) Describe() string {
	switch r {
	case RiskSend:
		return "send a message"
	case RiskPay:
		return "pay or buy"
	case RiskDelete:
		return "delete something"
	case RiskInstall:
		return "install or uninstall an app"
	case RiskCall:
		return "place a call"
	case RiskPublish:
		return "post or share"
	case RiskTransfer:
		return "move money"
	case RiskSubmit:
		return "submit a form"
	}
	return ""
}
