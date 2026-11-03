package goalcmd

import (
	"strings"
	"testing"
)

func TestContractAdvertisesSupportedCommandsAndAliases(t *testing.T) {
	want := []string{"start", "status", "inspect", "list", "ls", "logs", "log", "run", "stop", "resume", "unlock", "help", "-h", "--help"}
	valid := " " + ValidCommandText() + " "
	for _, name := range want {
		if !strings.Contains(valid, " "+name+" ") {
			t.Errorf("valid command text is missing %q: %s", name, valid)
		}
	}
	if strings.Contains(SlashDescription, "inspect") && !strings.Contains(valid, " inspect ") {
		t.Fatal("slash description advertises inspect without a matching alias")
	}
}

func TestBeginnerHelpExplainsLifecycle(t *testing.T) {
	help := HelpText()
	for _, want := range []string{
		"does not start execution",
		"active means runnable",
		"goal run <goal-id> --background",
		"goal logs <goal-id>",
		"logs <background-id>",
		"resume",
		"run it again",
		"/goal help",
	} {
		if !strings.Contains(help, want) {
			t.Errorf("help is missing %q:\n%s", want, help)
		}
	}
}

func TestChineseHelpExplainsGoalLifecycle(t *testing.T) {
	help := HelpText()
	for _, want := range []string{
		"中文说明",
		"不会开始执行",
		"active 表示可运行",
		"后台持续运行",
		"恢复状态",
		"Goal 生命周期事件",
		"后台任务输出",
		"/goal help",
		"English reference:",
	} {
		if !strings.Contains(help, want) {
			t.Errorf("Chinese help is missing %q:\n%s", want, help)
		}
	}
}

func TestChineseDescriptionsGuideBeginnersToGoalHelp(t *testing.T) {
	if SlashUsage != "/goal start|status|inspect|list|logs|run|stop|resume|unlock|help" {
		t.Fatalf("slash usage = %q", SlashUsage)
	}
	if HelpHintZH != "输入 /goal help 查看中文用法" {
		t.Fatalf("Chinese help hint = %q", HelpHintZH)
	}
	for name, description := range map[string]string{
		"global help": HelpDescriptionZH,
		"slash menu":  SlashDescription,
	} {
		for _, want := range []string{"管理长期目标", "/goal help"} {
			if !strings.Contains(description, want) {
				t.Errorf("%s description is missing %q: %q", name, want, description)
			}
		}
	}
}

func TestAccessorsReturnCopies(t *testing.T) {
	commands := Commands()
	commands[0].Name = "changed"
	commands[1].Aliases[0] = "changed-alias"
	got := Commands()
	if got[0].Name == "changed" || got[1].Aliases[0] == "changed-alias" {
		t.Fatal("Commands exposed mutable package state")
	}
}
