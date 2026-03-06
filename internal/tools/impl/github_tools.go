package impl

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/google/generative-ai-go/genai"
	"github.com/jackstrohm/jot/pkg/infra"
	"github.com/jackstrohm/jot/tools"
)

const githubAPIBase = "https://api.github.com"

func init() {
	registerGitHubTools()
}

func registerGitHubTools() {
	tools.Register(&tools.Tool{
		Name:        "github_read",
		Description: "Read-only GitHub operations: get repo info, file contents, open issues, open PRs, or README. Use for public repos; set GITHUB_TOKEN for private repos or higher rate limits.",
		Category:    "web",
		Params: []tools.Param{
			tools.EnumParam("action", "Action: 'repo_info', 'file_content', 'list_issues', 'list_prs', 'readme'", true, []string{"repo_info", "file_content", "list_issues", "list_prs", "readme"}),
			tools.RequiredStringParam("repo", "Repository as owner/name (e.g. octocat/Hello-World)"),
			tools.OptionalStringParam("path", "File path in repo (required for file_content, e.g. README.md or src/main.go)"),
			{
				Name:        "limit",
				Description: "Max items for list_issues/list_prs (default 10, max 30)",
				Type:        genai.TypeInteger,
				Required:    false,
				Default:     10,
				Min:         intPtr(1),
				Max:         intPtr(30),
			},
		},
		Execute: executeGitHubRead,
	})
}

func intPtr(n int) *int { return &n }

func executeGitHubRead(ctx context.Context, args *tools.Args) tools.Result {
	ctx, span := infra.StartSpan(ctx, "tool.github_read")
	defer span.End()

	action, ok := args.RequiredString("action")
	if !ok {
		return tools.MissingParam("action")
	}
	repo, ok := args.RequiredString("repo")
	if !ok {
		return tools.MissingParam("repo")
	}
	repo = strings.TrimSpace(repo)
	parts := strings.SplitN(repo, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return tools.Fail("repo must be owner/name (e.g. octocat/Hello-World)")
	}
	owner, repoName := parts[0], parts[1]

	path := args.String("path", "")
	limit := args.IntBounded("limit", 10, 1, 30)

	span.SetAttributes(map[string]string{"action": action, "repo": repo})

	switch action {
	case "repo_info":
		return githubRepoInfo(ctx, owner, repoName)
	case "file_content":
		if path == "" {
			return tools.MissingParam("path")
		}
		return githubFileContent(ctx, owner, repoName, path)
	case "list_issues":
		return githubListIssues(ctx, owner, repoName, limit)
	case "list_prs":
		return githubListPRs(ctx, owner, repoName, limit)
	case "readme":
		return githubReadme(ctx, owner, repoName)
	default:
		return tools.Fail("unknown action: %s", action)
	}
}

func githubClient() *http.Client {
	return &http.Client{Timeout: 15 * time.Second}
}

func githubReq(ctx context.Context, method, path string) (*http.Request, error) {
	u := githubAPIBase + path
	req, err := http.NewRequestWithContext(ctx, method, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return req, nil
}

func githubDo(ctx context.Context, method, path string) ([]byte, int, error) {
	req, err := githubReq(ctx, method, path)
	if err != nil {
		return nil, 0, err
	}
	resp, err := githubClient().Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return body, resp.StatusCode, nil
}

func githubRepoInfo(ctx context.Context, owner, repo string) tools.Result {
	path := fmt.Sprintf("/repos/%s/%s", url.PathEscape(owner), url.PathEscape(repo))
	body, status, err := githubDo(ctx, http.MethodGet, path)
	if err != nil {
		return tools.Fail("GitHub request failed: %v", err)
	}
	if status != http.StatusOK {
		return tools.Fail("GitHub API returned %d: %s", status, string(body))
	}
	var v struct {
		FullName    string `json:"full_name"`
		Description string `json:"description"`
		HTMLURL     string `json:"html_url"`
		DefaultBranch string `json:"default_branch"`
		Stars       int    `json:"stargazers_count"`
		Forks       int    `json:"forks_count"`
		OpenIssues  int    `json:"open_issues_count"`
		Language    string `json:"language"`
		CreatedAt   string `json:"created_at"`
		UpdatedAt   string `json:"updated_at"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		return tools.Fail("failed to parse repo response: %v", err)
	}
	out := fmt.Sprintf("Repository: %s\nURL: %s\nDefault branch: %s\nDescription: %s\nStars: %d | Forks: %d | Open issues: %d\nLanguage: %s\nCreated: %s | Updated: %s",
		v.FullName, v.HTMLURL, v.DefaultBranch, v.Description, v.Stars, v.Forks, v.OpenIssues, v.Language, v.CreatedAt, v.UpdatedAt)
	return tools.OK("%s", out)
}

func githubFileContent(ctx context.Context, owner, repo, path string) tools.Result {
	pathEnc := url.PathEscape(owner) + "/" + url.PathEscape(repo) + "/contents/" + url.PathEscape(path)
	body, status, err := githubDo(ctx, http.MethodGet, "/repos/"+pathEnc)
	if err != nil {
		return tools.Fail("GitHub request failed: %v", err)
	}
	if status != http.StatusOK {
		return tools.Fail("GitHub API returned %d: %s", status, string(body))
	}
	var v struct {
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
		Name     string `json:"name"`
		SHA      string `json:"sha"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		return tools.Fail("failed to parse file response: %v", err)
	}
	if v.Encoding != "base64" {
		return tools.Fail("unsupported encoding: %s", v.Encoding)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(v.Content, "\n", ""))
	if err != nil {
		return tools.Fail("failed to decode base64 content: %v", err)
	}
	content := string(decoded)
	const maxLen = 50000
	if len(content) > maxLen {
		content = content[:maxLen] + "\n...[truncated]"
	}
	return tools.OK("File: %s (sha: %s)\n\n%s", v.Name, v.SHA, content)
}

func githubListIssues(ctx context.Context, owner, repo string, limit int) tools.Result {
	path := fmt.Sprintf("/repos/%s/%s/issues?state=open&per_page=%d", url.PathEscape(owner), url.PathEscape(repo), limit)
	body, status, err := githubDo(ctx, http.MethodGet, path)
	if err != nil {
		return tools.Fail("GitHub request failed: %v", err)
	}
	if status != http.StatusOK {
		return tools.Fail("GitHub API returned %d: %s", status, string(body))
	}
	var list []struct {
		Number  int    `json:"number"`
		Title   string `json:"title"`
		State   string `json:"state"`
		HTMLURL string `json:"html_url"`
		User    struct {
			Login string `json:"login"`
		} `json:"user"`
		PullRequest interface{} `json:"pull_request"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		return tools.Fail("failed to parse issues response: %v", err)
	}
	var lines []string
	for _, i := range list {
		if i.PullRequest != nil {
			continue
		}
		lines = append(lines, fmt.Sprintf("#%d %s\n  %s\n  by %s", i.Number, i.Title, i.HTMLURL, i.User.Login))
	}
	if len(lines) == 0 {
		return tools.OK("No open issues found.")
	}
	return tools.OK("Open issues (%d):\n%s", len(lines), strings.Join(lines, "\n\n"))
}

func githubListPRs(ctx context.Context, owner, repo string, limit int) tools.Result {
	path := fmt.Sprintf("/repos/%s/%s/pulls?state=open&per_page=%d", url.PathEscape(owner), url.PathEscape(repo), limit)
	body, status, err := githubDo(ctx, http.MethodGet, path)
	if err != nil {
		return tools.Fail("GitHub request failed: %v", err)
	}
	if status != http.StatusOK {
		return tools.Fail("GitHub API returned %d: %s", status, string(body))
	}
	var list []struct {
		Number  int    `json:"number"`
		Title   string `json:"title"`
		State   string `json:"state"`
		HTMLURL string `json:"html_url"`
		User    struct {
			Login string `json:"login"`
		} `json:"user"`
		Head struct {
			Ref string `json:"ref"`
		} `json:"head"`
		Base struct {
			Ref string `json:"ref"`
		} `json:"base"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		return tools.Fail("failed to parse pulls response: %v", err)
	}
	var lines []string
	for _, p := range list {
		lines = append(lines, fmt.Sprintf("#%d %s\n  %s -> %s\n  %s\n  by %s", p.Number, p.Title, p.Head.Ref, p.Base.Ref, p.HTMLURL, p.User.Login))
	}
	if len(lines) == 0 {
		return tools.OK("No open pull requests found.")
	}
	return tools.OK("Open pull requests (%d):\n%s", len(lines), strings.Join(lines, "\n\n"))
}

func githubReadme(ctx context.Context, owner, repo string) tools.Result {
	path := fmt.Sprintf("/repos/%s/%s/readme", url.PathEscape(owner), url.PathEscape(repo))
	body, status, err := githubDo(ctx, http.MethodGet, path)
	if err != nil {
		return tools.Fail("GitHub request failed: %v", err)
	}
	if status != http.StatusOK {
		return tools.Fail("GitHub API returned %d (no README or repo not found): %s", status, string(body))
	}
	var v struct {
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
		Name     string `json:"name"`
		HTMLURL  string `json:"html_url"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		return tools.Fail("failed to parse readme response: %v", err)
	}
	if v.Encoding != "base64" {
		return tools.Fail("unsupported encoding: %s", v.Encoding)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(v.Content, "\n", ""))
	if err != nil {
		return tools.Fail("failed to decode base64 content: %v", err)
	}
	content := string(decoded)
	const maxLen = 50000
	if len(content) > maxLen {
		content = content[:maxLen] + "\n...[truncated]"
	}
	return tools.OK("README (%s): %s\n\n%s", v.Name, v.HTMLURL, content)
}
