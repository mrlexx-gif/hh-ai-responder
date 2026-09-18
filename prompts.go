package main

import (
	"embed"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"text/template"
	"time"
)

// Default prompt templates are embedded into the binary so the application works
// out of the box, without any files on disk.
//
// Users can customize the AI behaviour by putting *.md files into a prompts
// directory (see -prompts-dir / HH_PROMPTS_DIR):
//
//   - chat.md      — fully replaces the built-in chat reply system prompt;
//   - chat_user.md — fully replaces the built-in chat reply user prompt;
//   - letter.md    — fully replaces the built-in cover letter system prompt;
//   - solution.md  — fully replaces the built-in test solution system prompt;
//   - any other *.md file — appended as extra context to every system prompt.
//
// Templates support Go text/template syntax; available variables are documented
// on ChatPromptData, ChatUserPromptData, LetterPromptData and SolutionPromptData.
//
//go:embed prompts/*.md
var embeddedPrompts embed.FS

const (
	promptKindChat     = "chat"
	promptKindChatUser = "chat_user"
	promptKindLetter   = "letter"
	promptKindSolution = "solution"

	promptFileExt     = ".md"
	defaultPromptsDir = "prompts"
	promptsScanPeriod = 30 * time.Second
)

// promptKinds lists the templates that can be overridden by a file with the
// same base name, e.g. chat.md.
var promptKinds = []string{promptKindChat, promptKindChatUser, promptKindLetter, promptKindSolution}

// ChatPromptData is the template context of the chat reply system prompt.
type ChatPromptData struct {
	FirstName   string
	LastName    string
	ResumeTitle string
	Salary      string
	Skills      string
	Experience  string
}

// ChatUserPromptData is the template context of the chat reply user prompt,
// i.e. the message that is sent to the model on behalf of the candidate.
type ChatUserPromptData struct {
	// Message is the trimmed text of the employer's last message.
	Message string
	// ReplyOptions lists the answer variants when the employer asks to pick one.
	// When non-empty the model must answer with one of them verbatim.
	ReplyOptions []string
	VacancyName  string
	Salary       string
	CompanyName  string
	ContactName  string
	GithubURL    string
	// ChatHistory is the formatted conversation history, may be empty.
	ChatHistory string
	Contacts    string
	Extra       string
}

// LetterPromptData is the template context of the cover letter system prompt.
type LetterPromptData struct {
	FullName    string
	ResumeTitle string
	Salary      string
	Skills      string
	Experience  string
	Contacts    string
	Extra       string
}

// SolutionPromptData is the template context of the test solution system prompt.
type SolutionPromptData struct {
	GithubURL string
	Contacts  string
	Extra     string
}

// PromptManager loads user-provided *.md files and renders prompt templates.
// It is safe for concurrent use and periodically re-reads the prompts directory
// so that context files can be edited without restarting the application.
type PromptManager struct {
	dir string

	mu        sync.RWMutex
	templates map[string]*template.Template
	global    string
	lastScan  time.Time
}

// NewPromptManager creates a manager bound to dir. An empty dir falls back to
// the defaultPromptsDir directory. Missing files are not an error: embedded
// defaults are used instead.
func NewPromptManager(dir string) *PromptManager {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		dir = defaultPromptsDir
	}

	pm := &PromptManager{
		dir:       dir,
		templates: make(map[string]*template.Template, len(promptKinds)),
	}

	if err := pm.Reload(); err != nil && logger != nil {
		logger.Warn("Can't load prompts from %s: %v", dir, err)
	}

	return pm
}

// Reload re-reads prompt templates and extra context from disk. On success the
// loaded content atomically replaces the previous one.
func (pm *PromptManager) Reload() error {
	templates := make(map[string]*template.Template, len(promptKinds))
	for _, kind := range promptKinds {
		tmpl, err := pm.loadTemplate(kind)
		if err != nil {
			return err
		}
		templates[kind] = tmpl
	}

	global, err := pm.loadGlobalContext()
	if err != nil {
		return err
	}

	pm.mu.Lock()
	pm.templates = templates
	pm.global = global
	pm.lastScan = time.Now()
	pm.mu.Unlock()

	return nil
}

// Chat renders the chat reply system prompt.
func (pm *PromptManager) Chat(data ChatPromptData) string {
	return pm.render(promptKindChat, data)
}

// ChatUser renders the chat reply user prompt, i.e. the message sent on behalf
// of the candidate. Unlike system prompts it is not extended with the global
// context files, which are meant for system prompts only.
func (pm *PromptManager) ChatUser(data ChatUserPromptData) string {
	return strings.TrimSpace(pm.renderTemplate(promptKindChatUser, data))
}

// Letter renders the cover letter system prompt.
func (pm *PromptManager) Letter(data LetterPromptData) string {
	return pm.render(promptKindLetter, data)
}

// Solution renders the test solution system prompt.
func (pm *PromptManager) Solution(data SolutionPromptData) string {
	return pm.render(promptKindSolution, data)
}

func (pm *PromptManager) render(kind string, data any) string {
	return joinPromptParts(pm.renderTemplate(kind, data), pm.globalContext())
}

// renderTemplate executes the template registered for kind. Templates are read
// under a read lock so they can be reloaded concurrently.
func (pm *PromptManager) renderTemplate(kind string, data any) string {
	pm.maybeReload()

	pm.mu.RLock()
	tmpl := pm.templates[kind]
	pm.mu.RUnlock()

	if tmpl == nil {
		return ""
	}

	var sb strings.Builder
	if err := tmpl.Execute(&sb, data); err != nil {
		if logger != nil {
			logger.Warn("Can't render %s prompt: %v", kind, err)
		}
		return ""
	}

	return sb.String()
}

// globalContext returns the extra context assembled from non-prompt *.md files.
func (pm *PromptManager) globalContext() string {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	return pm.global
}

// maybeReload re-reads the prompts directory at most once per promptsScanPeriod.
func (pm *PromptManager) maybeReload() {
	if pm.dir == "" {
		return
	}

	pm.mu.RLock()
	due := time.Since(pm.lastScan) >= promptsScanPeriod
	pm.mu.RUnlock()

	if !due {
		return
	}

	if err := pm.Reload(); err != nil && logger != nil {
		logger.Warn("Can't reload prompts from %s: %v", pm.dir, err)
	}
}

// loadTemplate returns the user override for kind when it exists and is valid,
// otherwise the embedded default.
func (pm *PromptManager) loadTemplate(kind string) (*template.Template, error) {
	defaultContent, err := embeddedPrompts.ReadFile("prompts/" + kind + promptFileExt)
	if err != nil {
		return nil, err
	}

	if path, ok := pm.findUserPrompt(kind); ok {
		data, err := os.ReadFile(path)
		if err != nil {
			if logger != nil {
				logger.Warn("Can't read prompt file %s: %v", path, err)
			}
		} else if tmpl, err := parsePromptTemplate(kind, string(data)); err == nil {
			if logger != nil {
				logger.Debug("Using custom %s prompt template: %s", kind, path)
			}
			return tmpl, nil
		} else if logger != nil {
			logger.Warn("Invalid %s prompt template %s: %v (using built-in default)", kind, path, err)
		}
	}

	return parsePromptTemplate(kind, string(defaultContent))
}

// findUserPrompt resolves a case-insensitive <kind>.md file inside the prompts
// directory.
func (pm *PromptManager) findUserPrompt(kind string) (string, bool) {
	if pm.dir == "" {
		return "", false
	}

	entries, err := os.ReadDir(pm.dir)
	if err != nil {
		return "", false
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		if !strings.EqualFold(filepath.Ext(name), promptFileExt) {
			continue
		}

		base := strings.TrimSuffix(name, filepath.Ext(name))
		if strings.EqualFold(base, kind) {
			return filepath.Join(pm.dir, name), true
		}
	}

	return "", false
}

// loadGlobalContext concatenates every *.md file that is not a known prompt
// template (chat/letter/solution) into a single extra-context block.
func (pm *PromptManager) loadGlobalContext() (string, error) {
	if pm.dir == "" {
		return "", nil
	}

	entries, err := os.ReadDir(pm.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}

	var parts []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		if !strings.EqualFold(filepath.Ext(name), promptFileExt) {
			continue
		}

		base := strings.ToLower(strings.TrimSuffix(name, filepath.Ext(name)))
		if slices.Contains(promptKinds, base) {
			continue
		}

		data, err := os.ReadFile(filepath.Join(pm.dir, name))
		if err != nil {
			return "", err
		}

		if text := strings.TrimSpace(string(data)); text != "" {
			parts = append(parts, text)
		}
	}

	return strings.Join(parts, "\n\n"), nil
}

func parsePromptTemplate(kind, content string) (*template.Template, error) {
	return template.New(kind).Option("missingkey=zero").Parse(content)
}

// joinPromptParts joins non-empty parts with a blank line between them.
func joinPromptParts(parts ...string) string {
	cleaned := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			cleaned = append(cleaned, trimmed)
		}
	}
	return strings.Join(cleaned, "\n\n")
}
