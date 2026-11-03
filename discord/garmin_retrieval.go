package discord

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/MetrolistGroup/metrobot/cmd"
	"golang.org/x/net/idna"
)

const garminCredentialLabelPattern = `(?:(?:(?:access|refresh|id)[ _-]?)?token|password|passwd|passphrase|secret|api[ _-]?key|client[ _-]?secret|authorization|cookie|credentials?|session[ _-]?(?:id|key))`

var (
	garminSensitivePublicQueryPattern = regexp.MustCompile(`(?i)(?:\b` + garminCredentialLabelPattern + `["']?\s*(?:[:=]|\bis\b)|\bauthorization\s+(?:bearer|basic)\b|(?m:^\s*bearer\s+\S+)|[?&](?:code|state)=|https?://[^\s/]+@|<@!?\d+>|[a-z0-9._%+-]+@[a-z0-9.-]+\.[a-z]{2,}|\b(?:sk-[a-z0-9_-]{16,}|gh[pousr]_[a-z0-9]{16,}|github_pat_[a-z0-9_]{16,})\b)`)
	garminCredentialDisclosurePattern = regexp.MustCompile(`(?i)\b(?:my|our|your|his|her|their)[\s\p{P}\p{Z}\p{Cf}]+` + garminCredentialLabelPattern + `\b`)
	garminCredentialValuePattern      = regexp.MustCompile(`(?i)\b(?:` + garminCredentialLabelPattern + `|bearer)["']?\s+(?:"[^"\r\n]+"|'[^'\r\n]+'|[^\s"']*[0-9+/=_-][^\s"']*)`)
	// Ambiguous password/key phrases fail closed; ask for a credential-free public subject.
	garminPlainCredentialPattern  = regexp.MustCompile(`(?i)\b(?:bearer|password|passwd|passphrase|api[ _-]?key|client[ _-]?secret|(?:access|refresh|id)[ _-]?token|session[ _-]?key)\b`)
	garminBasicCredentialsPattern = regexp.MustCompile(`(?i)\bbasic\s+["']?([a-z0-9+/]+={0,2})`)
)

func garminSensitivePublicLookupText(text string) bool {
	// Also recognize credential labels inside common environment-variable spellings.
	for _, candidate := range []string{text, strings.ReplaceAll(strings.ReplaceAll(text, "_", " "), "-", " ")} {
		if garminSensitivePublicQueryPattern.MatchString(candidate) || garminCredentialDisclosurePattern.MatchString(candidate) || garminCredentialValuePattern.MatchString(candidate) || garminPlainCredentialPattern.MatchString(candidate) {
			return true
		}
	}
	// Basic credentials encode user:password; ordinary phrases such as "basic arithmetic" do not.
	for _, match := range garminBasicCredentialsPattern.FindAllStringSubmatch(text, -1) {
		decoded, err := base64.StdEncoding.DecodeString(match[1])
		if err != nil {
			decoded, err = base64.RawStdEncoding.DecodeString(match[1])
		}
		if err == nil && strings.Contains(string(decoded), ":") {
			return true
		}
	}
	return false
}

const garminDeviceSourceLabelPattern = `(?:gsmarena|nanoreview|technical[ .]city)`

// shortcut: only direct source exclusions are recognized; expand for a concrete rejected request.
var garminExcludedDeviceSourcePattern = regexp.MustCompile(`(?i)\b(?:not(?: from)?|without|excluding|except|skip|avoid|(?:do not|don't) use|rather than)\s+` + garminDeviceSourceLabelPattern + `\b(?:\s*(?:,|or|and)\s*` + garminDeviceSourceLabelPattern + `\b)*`)

var garminRequestedDeviceSourcePattern = regexp.MustCompile(`(?i)\b(?:show(?: me)?|find|use|using|from|on|via|also)\s+(?:the\s+)?(?:not only\s+)?` + garminDeviceSourceLabelPattern + `\b(?:\s*(?:,|and|or|but also)\s*` + garminDeviceSourceLabelPattern + `\b)*|\b` + garminDeviceSourceLabelPattern + `\s+(?:pages?|links?|specifications?|specs?|benchmarks?|results?)\b`)

func garminRequiredDeviceTools(messages []cmd.GarminAIMessage) []string {
	prompt := garminExcludedDeviceSourcePattern.ReplaceAllString(strings.ToLower(garminUserText(messages)), "")
	var required []string
	for _, request := range garminRequestedDeviceSourcePattern.FindAllString(prompt, -1) {
		for _, source := range []struct{ tool, label string }{
			{"get_gsmarena_phone", "gsmarena"},
			{"get_nanoreview_device", "nanoreview"},
			{"get_technicalcity_device", "technical.city"},
			{"get_technicalcity_device", "technical city"},
		} {
			if strings.Contains(request, source.label) && !slices.Contains(required, source.tool) {
				required = append(required, source.tool)
			}
		}
	}
	if len(required) > 0 {
		return required
	}
	name := ""
	if containsAnyGarminPhrase(prompt, "spec", "benchmark", "performance", "compare", "chipset", "processor", "soc", "battery", "charging", "display", "screen", "camera", "dimensions", "weight", "ram", "storage", "network bands", "release date", "vram", "clock", "cores", "memory", "tdp") {
		name = garminHardwareToolRequested(messages)
		if name == "" && garminGSMArenaRequested(messages) {
			name = "get_gsmarena_phone"
		}
	}
	pageRequested := containsAnyGarminPhrase(prompt, "show", "find", "look up", "lookup", "page", "link", "url", "http://", "https://")
	if name == "" && strings.Contains(prompt, "gsmarena") && (garminGSMArenaPhoneNamed(prompt) || pageRequested) {
		name = "get_gsmarena_phone"
	}
	if name == "" && pageRequested {
		name = garminHardwareToolRequested(messages)
	}
	if name == "" {
		return nil
	}
	return []string{name}
}

var garminDevicePageURLPattern = regexp.MustCompile("(?i)https?://[^\\s<>\"`]+")

func garminDevicePageURLsGrounded(answer string, verified map[string]struct{}, linkSources []string, checkOtherLinks bool) bool {
	// shortcut: URL grounding does not verify model-to-page matches or prose claims; add semantic checks only for a concrete failing case.
	for _, raw := range garminDevicePageURLPattern.FindAllString(answer, -1) {
		raw = strings.TrimRight(raw, ".,!?;:)]}")
		if _, ok := verified[raw]; ok {
			continue
		}
		parsed, err := url.Parse(raw)
		if err != nil {
			return false
		}
		host, err := idna.Lookup.ToASCII(parsed.Hostname())
		if err != nil {
			return false
		}
		switch strings.TrimPrefix(strings.TrimSuffix(strings.ToLower(host), "."), "www.") {
		case "gsmarena.com", "nanoreview.net", "technical.city":
			return false
		}
		if !checkOtherLinks {
			continue
		}
		// Other citations may come from the current request or permitted public/private results.
		if !slices.ContainsFunc(linkSources, func(source string) bool {
			if strings.Contains(source, mustJSON(raw)) {
				return true
			}
			return slices.ContainsFunc(garminDevicePageURLPattern.FindAllString(source, -1), func(candidate string) bool {
				return strings.TrimRight(candidate, ".,!?;:)]}") == raw
			})
		}) {
			return false
		}
	}
	return true
}

func garminHardwareToolRequested(messages []cmd.GarminAIMessage) string {
	prompt := garminExcludedDeviceSourcePattern.ReplaceAllString(strings.ToLower(garminUserText(messages)), "")
	if strings.Contains(prompt, "nanoreview") {
		return "get_nanoreview_device"
	}
	if strings.Contains(prompt, "technical.city") || strings.Contains(prompt, "technical city") {
		return "get_technicalcity_device"
	}
	if !containsAnyGarminPhrase(prompt, "spec", "benchmark", "performance", "how much", "how many", "what about", "its ", "compare", "vram", "clock", "cores", "memory", "tdp") {
		return ""
	}
	if containsAnyGarminPhrase(prompt, "rtx ", "gtx ", "geforce ", "radeon ", "ryzen ", "threadripper ", "intel core ", "core ultra ", "epyc ") {
		return "get_technicalcity_device"
	}
	if containsAnyGarminPhrase(prompt, "snapdragon ", "dimensity ", "exynos ", "apple m1", "apple m2", "apple m3", "apple m4", "apple m5") && !garminGSMArenaPhoneNamed(prompt) {
		return "get_nanoreview_device"
	}
	for index := len(messages) - 2; index >= 0; index-- {
		for _, call := range messages[index].ToolCalls {
			if call.Function.Name == "get_nanoreview_device" || call.Function.Name == "get_technicalcity_device" {
				return call.Function.Name
			}
		}
	}
	return ""
}

func garminHardwareQuery(messages []cmd.GarminAIMessage, name string) string {
	query := strings.TrimSpace(garminUserText(messages))
	for _, field := range strings.Fields(query) {
		if strings.HasPrefix(field, "https://nanoreview.net/en/") || strings.HasPrefix(field, "https://technical.city/en/") {
			return strings.TrimRight(field, ",.?!>)")
		}
	}
	// Follow-up questions retain the last product rather than searching for
	// phrases like "what about its memory" as a new model name.
	if !containsAnyGarminPhrase(strings.ToLower(query), "nanoreview", "technical.city", "technical city", "rtx ", "gtx ", "geforce ", "radeon ", "ryzen ", "threadripper ", "intel core ", "core ultra ", "epyc ", "snapdragon ", "dimensity ", "exynos ", "apple m1", "apple m2", "apple m3", "apple m4", "apple m5") {
		for index := len(messages) - 2; index >= 0; index-- {
			for _, call := range messages[index].ToolCalls {
				if call.Function.Name == name {
					var args garminToolArgs
					if json.Unmarshal([]byte(call.Function.Arguments), &args) == nil && args.Query != "" {
						return args.Query
					}
				}
			}
		}
	}
	stop := map[string]bool{"garmin": true, "metrobot": true, "search": true, "look": true, "up": true, "find": true, "show": true, "give": true, "me": true, "the": true, "a": true, "an": true, "for": true, "of": true, "on": true, "from": true, "in": true, "please": true, "what": true, "whats": true, "are": true, "is": true, "does": true, "do": true, "tell": true, "about": true, "specs": true, "specifications": true, "benchmark": true, "benchmarks": true, "performance": true, "nanoreview": true, "technical": true, "city": true, "technical.city": true, "technicalcity": true, "cpu": true, "gpu": true, "phone": true, "laptop": true, "soc": true, "device": true, "graphics": true, "card": true, "details": true, "information": true, "with": true, "how": true, "many": true, "much": true, "vram": true, "has": true, "have": true, "memory": true, "clock": true, "cores": true, "frequency": true, "tdp": true}
	var model []string
	for _, field := range strings.Fields(query) {
		if !stop[strings.ToLower(strings.Trim(field, ",.?!:;"))] {
			model = append(model, field)
		}
	}
	return truncateRunes(strings.Trim(strings.Join(model, " "), " ,.?!"), 200)
}

func garminGSMArenaRequested(messages []cmd.GarminAIMessage) bool {
	prompt := garminExcludedDeviceSourcePattern.ReplaceAllString(strings.ToLower(garminUserText(messages)), "")
	if strings.Contains(prompt, "gsmarena") {
		return true
	}
	phone := garminGSMArenaPhoneNamed(prompt)
	specs := containsAnyGarminPhrase(prompt,
		"specs", "specifications", "chipset", "processor", "soc", "battery", "charging", "display", "screen", "camera",
		"dimensions", "weight", "ram", "storage", "network bands", "release date")
	if phone && specs {
		return true
	}
	if specs {
		for index := len(messages) - 2; index >= 0; index-- {
			for _, call := range messages[index].ToolCalls {
				if call.Function.Name == "get_gsmarena_phone" {
					return true
				}
			}
		}
	}
	return false
}

func garminGSMArenaPhoneNamed(prompt string) bool {
	return containsAnyGarminPhrase(strings.ToLower(prompt),
		"phone", "smartphone", "iphone", "pixel", "galaxy", "samsung", "oneplus", "xiaomi", "redmi", "poco",
		"oppo", "vivo", "realme", "motorola", "nokia", "xperia", "nothing phone", "honor", "huawei", "tecno", "infinix")
}

func garminGSMArenaQuery(messages []cmd.GarminAIMessage) string {
	query := garminUserText(messages)
	if garminGSMArenaPhoneNamed(query) {
		return truncateRunes(query, 200)
	}
	for index := len(messages) - 2; index >= 0; index-- {
		for _, call := range messages[index].ToolCalls {
			if call.Function.Name != "get_gsmarena_phone" {
				continue
			}
			var args struct {
				Query string `json:"query"`
			}
			if json.Unmarshal([]byte(call.Function.Arguments), &args) == nil && strings.TrimSpace(args.Query) != "" {
				return truncateRunes(args.Query+" "+query, 200)
			}
		}
	}
	return truncateRunes(query, 200)
}

func garminMathRequested(prompt string) bool {
	prompt = strings.ToLower(prompt)
	if !strings.ContainsAny(prompt, "0123456789⁰¹²³⁴⁵⁶⁷⁸⁹") {
		return false
	}
	return strings.ContainsAny(prompt, "+*/^×÷%=") || garminAIMathSubtractionPattern.MatchString(prompt) || strings.ContainsAny(prompt, "⁰¹²³⁴⁵⁶⁷⁸⁹") || containsAnyGarminPhrase(prompt,
		"calculate", "arithmetic", "equation", "solve", "how much", "percent of", " plus ", " minus ", " times ", "divided by", "squared", "cubed")
}

func garminWebSearchRequested(prompt string) bool {
	prompt = strings.ToLower(prompt)
	if garminExplicitWebSearchRequested(prompt) {
		return true
	}
	// Channel retrieval and incidental words like "find" or "latest" are not public-search requests.
	if containsAnyGarminPhrase(prompt, "messages", "channel history", "conversation", "chat history", "who said", "what did we", "what we discussed") {
		return false
	}
	if !strings.Contains(prompt, "?") && !containsAnyGarminPhrase(prompt, "what ", "what's ", "whats ", "who ", "when ", "where ", "how ", "tell me ", "show me ", "give me ", "get me ", "find ", "any news", "news about", "weather in") {
		return false
	}
	padded := " " + prompt + " "
	return containsAnyGarminPhrase(prompt, "news", "weather", "current price", "direct link", "latest release", "latest version") ||
		containsAnyGarminPhrase(padded, " link ", " url ", " gif ", " image ", " images ", " website ")
}

func garminPublicWebSearchRequested(prompt string) bool {
	return containsAnyGarminPhrase(prompt,
		"search the web", "search web", "web search", "browse the web", "browse web", "browse the internet",
		"search online", "look online", "google ")
}

func garminExplicitWebSearchRequested(prompt string) bool {
	return garminPublicWebSearchRequested(prompt) || containsAnyGarminPhrase(prompt,
		"look up ", "lookup ", "look it up", "look this up", "image of", "image search", "find an image", "tenor") ||
		strings.Contains(prompt, "http://") || strings.Contains(prompt, "https://")
}

func garminWebSearchSource(prompt string) string {
	prompt = strings.ToLower(prompt)
	if strings.Contains(prompt, "tenor") {
		return "web"
	}
	padded := " " + prompt + " "
	if containsAnyGarminPhrase(prompt, "image of", "image search", "find an image", "google images", "photo of", "picture of") ||
		containsAnyGarminPhrase(padded, " image ", " images ", " gif ") {
		return "images"
	}
	if containsAnyGarminPhrase(prompt, "latest story", "recent story", "headline", "news") {
		return "news"
	}
	return "web"
}

func garminWebSearchQuery(messages []cmd.GarminAIMessage) string {
	query := strings.TrimSpace(garminUserText(messages))
	switch strings.Trim(strings.ToLower(query), " .,?!") {
	case "look it up", "look this up", "look that up", "use google images yourself", "google images":
		return ""
	}
	if garminAIContextOnlyQueryPattern.MatchString(query) {
		return ""
	}
	if strings.Contains(strings.ToLower(query), "tenor") && !strings.Contains(strings.ToLower(query), "site:tenor.com") {
		query = "site:tenor.com " + query
	}
	return truncateRunes(query, 500)
}

func garminWebSearchResultInstruction(prompt string) string {
	instruction := "\n\nA live web search succeeded. Answer from the supplied result, include relevant direct URLs, and do not claim web access is unavailable."
	if strings.Contains(strings.ToLower(prompt), "tenor") {
		return instruction + " Return a tenor.com result URL."
	}
	if garminWebSearchSource(prompt) == "images" {
		return instruction + " Use the first relevant imageUrl. If the user requested only a link, output only that raw URL."
	}
	return instruction
}

func garminRepositoryRequested(prompt string) bool {
	githubSearch := strings.Contains(prompt, "github") && containsAnyGarminPhrase(prompt, "search", "find", "look", "browse")
	repositoryAction := containsAnyGarminPhrase(prompt, "github", "search", "find", "look", "show", "describe", "description", "about", "details", "what", "stars", "forks", "language", "topic", "change", "commit", "file", "source", "code", "read")
	return githubSearch || (garminRepositorySubject(prompt) && repositoryAction)
}

func garminRepositorySubject(prompt string) bool {
	if garminHasGitHubRepositoryReference(prompt) {
		return true
	}
	for _, field := range strings.Fields(prompt) {
		switch strings.Trim(strings.ToLower(field), "<>[](){}.,!?;:\"'") {
		case "repo", "repos", "repository", "repositories":
			return true
		case "project", "projects":
			if strings.Contains(prompt, "github") && containsAnyGarminPhrase(prompt, "search", "find", "look", "browse") {
				return true
			}
		}
	}
	return false
}

func garminHasGitHubRepositoryReference(prompt string) bool {
	if strings.Contains(prompt, "github.com/") {
		return true
	}
	for _, field := range strings.Fields(prompt) {
		field = strings.Trim(field, "<>[](){}.,!?;:\"'")
		parts := strings.Split(field, "/")
		if len(parts) == 2 && parts[0] != "" && parts[1] != "" {
			switch strings.ToLower(parts[0]) {
			case "r", "u", "user":
				if !containsAnyGarminPhrase(strings.ToLower(prompt), "github", "repo", "repository") {
					continue
				}
			}
			return true
		}
	}
	return false
}

func garminReadableChannelForConversation(messages []cmd.GarminAIMessage) string {
	prompt := strings.ToLower(garminUserText(messages))
	if containsAnyGarminPhrase(prompt, "minky", "minky channel", "elissa's cat", "elissa cat") {
		return "minky"
	}
	if containsAnyGarminPhrase(prompt, "polls channel", "in polls", "posted in polls", "latest poll", "recent poll") ||
		(strings.Contains(prompt, "poll") && containsAnyGarminPhrase(prompt, "design", "feature", "staff", "users", "vote")) {
		return "polls"
	}
	if containsAnyGarminPhrase(prompt, "sneak-peeks", "sneak peeks", "sneak peek") ||
		(strings.Contains(prompt, "kmp") && containsAnyGarminPhrase(prompt,
			"fake", "real", "rewrite", "progress", "status", "preview", "sneak", "posted", "showed")) {
		return "sneak-peeks"
	}
	if containsAnyGarminPhrase(prompt,
		"coolchannel", "cool channel", "sneak-peeks", "sneak peeks", "sneak peek", "kmp preview",
		"kmp previews", "maintainer channel", "maintainer chat", "maintainers posted", "maintainers said",
		"maintainers talking", "maintainers discussing", "maintainer shitpost", "maintainer shitposts") {
		return "coolchannel"
	}
	return ""
}

func garminUserText(messages []cmd.GarminAIMessage) string {
	for index := len(messages) - 1; index >= 0; index-- {
		if messages[index].Role == "user" {
			return strings.TrimSpace(messages[index].Content)
		}
	}
	return ""
}

func garminRememberRequested(content string) bool {
	content = strings.ToLower(strings.TrimSpace(content))
	if prompt, triggered := cmd.ExtractGarminPrompt(content); triggered {
		content = prompt
	}
	if end := strings.IndexByte(content, '>'); strings.HasPrefix(content, "<@") && end >= 0 && normalizeDiscordUserID(content[:end+1]) != "" {
		content = strings.TrimSpace(strings.TrimLeft(content[end+1:], ",:;-"))
	}
	for _, clause := range strings.Split(content, ",") {
		clause = strings.TrimSpace(clause)
		for _, prefix := range []string{"please ", "can you ", "could you ", "would you "} {
			clause = strings.TrimSpace(strings.TrimPrefix(clause, prefix))
		}
		clause = strings.TrimSpace(strings.TrimPrefix(clause, "please "))
		for _, phrase := range []string{
			"remember that", "remember this", "remember:", "save that to memory", "save this to memory",
			"save to memory", "add that to memory", "add this to memory", "add to memory",
		} {
			if strings.HasPrefix(clause, phrase) {
				return true
			}
		}
		if containsAnyGarminPhrase(clause, "do not", "don't", "never asked", "didn't ask", "not asking") {
			return false
		}
	}
	return false
}

func garminPerUserMemoryRequest(content string) bool {
	content = strings.ToLower(strings.TrimSpace(content))
	return containsAnyGarminPhrase(content,
		"remember me", "remember my", "remember that i", "remember that i'm", "remember that im",
		"save my profile", "save this about me", "forget me", "forget about me", "clear my profile",
		"clear my memory", "delete my memory", "personalization memory")
}

func containsAnyGarminPhrase(content string, phrases ...string) bool {
	for _, phrase := range phrases {
		if strings.Contains(content, phrase) {
			return true
		}
	}
	return false
}
