package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPromptManagerDefaults(t *testing.T) {
	pm := NewPromptManager(filepath.Join(t.TempDir(), "missing"))

	chat := pm.Chat(ChatPromptData{
		FirstName:   "Иван",
		LastName:    "Петров",
		ResumeTitle: "Go-разработчик",
		Salary:      "200000",
		Skills:      "Go",
		Experience:  "5 лет",
	})

	if !strings.Contains(chat, "Ты соискатель") {
		t.Fatalf("default chat prompt not used: %q", chat)
	}
	if !strings.Contains(chat, "Иван Петров") || !strings.Contains(chat, "Go-разработчик") {
		t.Fatalf("template data not rendered: %q", chat)
	}
}

func TestPromptManagerOverrideAndGlobalContext(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("chat.md", "CUSTOM {{ .FirstName }} / {{ .ResumeTitle }}")
	write("about_me.md", "GLOBAL CONTEXT LINE")

	pm := NewPromptManager(dir)
	chat := pm.Chat(ChatPromptData{FirstName: "Иван", ResumeTitle: "Go"})

	if !strings.Contains(chat, "CUSTOM Иван / Go") {
		t.Fatalf("override not applied: %q", chat)
	}
	if !strings.Contains(chat, "GLOBAL CONTEXT LINE") {
		t.Fatalf("global context not appended: %q", chat)
	}
	if strings.Contains(chat, "Ты соискатель") {
		t.Fatalf("default chat prompt should be replaced: %q", chat)
	}
}

func TestPromptManagerInvalidOverrideFallsBack(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "letter.md"), []byte("broken {{ .FullName"), 0o600); err != nil {
		t.Fatal(err)
	}

	pm := NewPromptManager(dir)
	letter := pm.Letter(LetterPromptData{FullName: "Test"})

	if !strings.Contains(letter, "сопроводительное письмо") {
		t.Fatalf("fallback default not used: %q", letter)
	}
}

func TestPromptManagerSolutionTemplate(t *testing.T) {
	// Point to a missing dir so the embedded default template is used.
	pm := NewPromptManager(filepath.Join(t.TempDir(), "missing"))
	sol := pm.Solution(SolutionPromptData{
		GithubURL: "https://github.com/s3rgeym",
		Contacts:  "+79990000000",
		Extra:     "EXTRA RULE",
	})

	for _, want := range []string{"https://github.com/s3rgeym", "+79990000000", "EXTRA RULE"} {
		if !strings.Contains(sol, want) {
			t.Fatalf("solution prompt missing %q: %q", want, sol)
		}
	}
}

func TestPromptManagerChatUserTemplate(t *testing.T) {
	// Point to a missing dir so the embedded default template is used.
	pm := NewPromptManager(filepath.Join(t.TempDir(), "missing"))

	user := pm.ChatUser(ChatUserPromptData{
		Message:     "Здравствуйте!",
		VacancyName: "Go-разработчик",
		Salary:      "200 000 руб",
		CompanyName: "ACME",
		ContactName: "Иван",
		GithubURL:   "https://github.com/s3rgeym",
		ChatHistory: "[2024-01-01 10:00:00] Иван\n---",
		Contacts:    "+79990000000",
		Extra:       "EXTRA RULE",
	})

	for _, want := range []string{
		"Здравствуйте!",
		"Название вакансии: Go-разработчик",
		"200 000 руб",
		"Компания: ACME",
		"https://github.com/s3rgeym",
		"История переписки:",
		"Твои контакты: +79990000000",
		"EXTRA RULE",
	} {
		if !strings.Contains(user, want) {
			t.Fatalf("chat user prompt missing %q: %q", want, user)
		}
	}

	opts := pm.ChatUser(ChatUserPromptData{
		Message:      "Выберите вариант",
		ReplyOptions: []string{"Да", "Нет"},
	})

	for _, want := range []string{"Варианты ответа:", "- Да", "- Нет"} {
		if !strings.Contains(opts, want) {
			t.Fatalf("chat user options missing %q: %q", want, opts)
		}
	}
	if strings.Contains(opts, "Название вакансии") {
		t.Fatalf("vacancy rules must be omitted when options are present: %q", opts)
	}
}
