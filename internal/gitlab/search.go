package gitlab

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"sync"

	gitlab "gitlab.com/gitlab-org/api/client-go"
)

type SearchOptions struct {
	Query    string
	File     string
	Projects []string
	Groups   []string
}

type SearchResult struct {
	Type          string // "CODE", "ISSUE", "MR"
	ProjectName   string
	ProjectID     int64
	ProjectWebURL string // Home link for the project
	Title         string
	FilePath      string // Only for CODE
	State         string
	URL           string // Direct link (with #L for CODE)
	ProjectURL    string // Search link for the project UI
}

type ProjectJob struct {
	Index   int
	Project *gitlab.Project
}

func (c *Client) SearchGitlab(opts SearchOptions) ([]SearchResult, error) {
	ctx := context.Background()

	// 1. Discover projects to search (needed for per-project blobs)
	projects, err := c.getProjects(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to discover projects: %w", err)
	}

	fmt.Printf("Searching for '%s' in %d projects...\n", opts.Query, len(projects))

	var allResults []SearchResult
	var mu sync.Mutex
	var wg sync.WaitGroup
	// Limit concurrency for blobs
	const workers = 10

	// 2. Parallel per-project Blob search (CE Compatibility)
	projChan := make(chan *ProjectJob, len(projects))
	resChan := make(chan []SearchResult, workers)

	// Create workers
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for pj := range projChan {
				p := pj.Project
				idx := pj.Index
				fmt.Printf("[%d/%d] %s\n", idx, len(projects), p.PathWithNamespace)
				res, err := c.searchBlobsByProject(ctx, p, opts.Query, opts.File)
				if err != nil {
					fmt.Printf("Warning: failed to search blobs in %s: %v\n", p.PathWithNamespace, err)
					return
				}
				resChan <- res
			}
		}()
	}

	// send jobs
	for idx, p := range projects {
		pj := &ProjectJob{
			Index:   idx,
			Project: p,
		}
		projChan <- pj
	}
	close(projChan)

	// 3. Global/Group search for Issues & MRs (Efficiency)
	wg.Add(1)
	go func() {
		defer wg.Done()
		enrichedRes := c.searchIssuesAndMRs(ctx, opts, projects)
		mu.Lock()
		allResults = append(allResults, enrichedRes...)
		mu.Unlock()
	}()

	// Wait for all searches to complete
	go func() {
		wg.Wait()
		close(resChan)
	}()

	for res := range resChan {
		mu.Lock()
		allResults = append(allResults, res...)
		mu.Unlock()
	}

	return allResults, nil
}

func (c *Client) searchBlobsByProject(ctx context.Context, p *gitlab.Project, query string, filePattern string) ([]SearchResult, error) {
	opt := &gitlab.SearchOptions{
		ListOptions: gitlab.ListOptions{
			PerPage: 100,
			Page:    1,
		},
	}

	var results []SearchResult
	for {
		if err := c.Limiter.Wait(ctx); err != nil {
			return nil, err
		}

		// BlobsByProject is CE compatible
		blobs, resp, err := c.Search.BlobsByProject(p.ID, query, opt)
		if err != nil {
			return nil, err
		}

		for _, b := range blobs {
			if filePattern != "" {
				matched, _ := regexp.MatchString(filePatternToRegex(filePattern), b.Filename)
				if !matched {
					continue
				}
			}

			fragment := ""
			if b.Startline > 0 {
				fragment = fmt.Sprintf("#L%d", b.Startline)
			}

			results = append(results, SearchResult{
				Type:        "CODE",
				ProjectName: p.PathWithNamespace,
				ProjectID:   p.ID,
				Title:       b.Filename,
				FilePath:    b.Filename,
				State:       b.Basename,
				URL:         fmt.Sprintf("%s/-/blob/%s/%s%s", p.WebURL, p.DefaultBranch, b.Filename, fragment),
				ProjectURL:  c.makeProjectSearchURL(p, query),
			})
		}

		if resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}
	return results, nil
}

func (c *Client) searchIssuesAndMRs(ctx context.Context, opts SearchOptions, projects []*gitlab.Project) []SearchResult {
	var res []SearchResult

	for _, group := range opts.Groups {
		issuesRes, err := c.searchIssues(ctx, group, opts.Query)
		if err != nil {
			fmt.Printf("Warning: failed to search issues in group %s: %v\n", group, err)
			continue
		}
		res = append(res, issuesRes...)

		mrsRes, err := c.searchMRs(ctx, group, opts.Query)
		if err != nil {
			fmt.Printf("Warning: failed to search MRs in group %s: %v\n", group, err)
			continue
		}
		res = append(res, mrsRes...)
	}

	if len(opts.Groups) == 0 {
		issuesRes, err := c.searchIssues(ctx, "", opts.Query)
		if err != nil {
			fmt.Printf("Warning: failed to search issues globally: %v\n", err)
		}
		res = append(res, issuesRes...)

		mrsRes, err := c.searchMRs(ctx, "", opts.Query)
		if err != nil {
			fmt.Printf("Warning: failed to search issues/MRs globally: %v\n", err)
		}
		res = append(res, mrsRes...)
	}

	// Create project map for fast lookup
	pMap := make(map[int64]*gitlab.Project)
	for _, p := range projects {
		pMap[p.ID] = p
	}

	// Filter and enrich global results
	var enrichedRes []SearchResult
	for _, r := range res {
		if p, ok := pMap[r.ProjectID]; ok {
			r.ProjectName = p.PathWithNamespace
			r.ProjectURL = c.makeProjectSearchURL(p, opts.Query)
			enrichedRes = append(enrichedRes, r)
		}
	}

	return enrichedRes
}

func (c *Client) searchIssues(ctx context.Context, groupID string, query string) ([]SearchResult, error) {
	var results []SearchResult

	// Issues
	opt := &gitlab.SearchOptions{
		ListOptions: gitlab.ListOptions{PerPage: 100, Page: 1},
	}
	for {
		if err := c.Limiter.Wait(ctx); err != nil {
			return nil, err
		}

		var issues []*gitlab.Issue
		var resp *gitlab.Response
		var err error

		if groupID == "" {
			issues, resp, err = c.Search.Issues(query, opt)

		} else {
			issues, resp, err = c.Search.IssuesByGroup(groupID, query, opt)
		}

		if err != nil {
			return nil, err
		}

		for _, i := range issues {
			results = append(results, SearchResult{
				Type:      "ISSUE",
				ProjectID: int64(i.ProjectID),
				Title:     i.Title,
				State:     i.State,
				URL:       i.WebURL,
				// ProjectName and ProjectURL will be resolved/filled later or fetched
			})
		}
		if resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}

	return results, nil
}

func (c *Client) searchMRs(ctx context.Context, groupID string, query string) ([]SearchResult, error) {

	var results []SearchResult

	// MRs
	opt := &gitlab.SearchOptions{
		ListOptions: gitlab.ListOptions{PerPage: 100, Page: 1},
	}

	for {

		if err := c.Limiter.Wait(ctx); err != nil {
			return nil, err
		}

		var mrs []*gitlab.MergeRequest
		var resp *gitlab.Response
		var err error

		if groupID == "" {
			mrs, resp, err = c.Search.MergeRequests(query, opt)
		} else {
			mrs, resp, err = c.Search.MergeRequestsByGroup(groupID, query, opt)
		}

		if err != nil {
			return nil, err
		}

		for _, m := range mrs {
			results = append(results, SearchResult{
				Type:      "MR",
				ProjectID: int64(m.ProjectID),
				Title:     m.Title,
				State:     m.State,
				URL:       m.WebURL,
			})
		}
		if resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}

	return results, nil
}

func (c *Client) makeProjectSearchURL(p *gitlab.Project, query string) string {
	u, _ := url.Parse(p.WebURL)
	// Example: https://{host}/search?search=axios%40&project_id=929&scope=blobs
	return fmt.Sprintf("%s://%s/search?search=%s&project_id=%d&scope=blobs",
		u.Scheme, u.Host, url.QueryEscape(query), p.ID)
}

func filePatternToRegex(pattern string) string {
	r := regexp.QuoteMeta(pattern)
	r = strings.ReplaceAll(r, "\\*", ".*")
	r = strings.ReplaceAll(r, "\\?", ".")
	return "^" + r + "$"
}

func (c *Client) getProjects(ctx context.Context, opts SearchOptions) ([]*gitlab.Project, error) {
	if len(opts.Projects) == 0 && len(opts.Groups) == 0 {
		return c.listAllProjects(ctx)
	}

	projectMap := make(map[int64]*gitlab.Project)

	for _, groupPath := range opts.Groups {
		projects, err := c.listGroupProjects(ctx, groupPath)
		if err != nil {
			return nil, fmt.Errorf("failed to list projects for group %s: %w", groupPath, err)
		}
		for _, p := range projects {
			projectMap[p.ID] = p
		}
	}

	for _, projectPath := range opts.Projects {
		if err := c.Limiter.Wait(ctx); err != nil {
			return nil, err
		}
		p, _, err := c.Projects.GetProject(projectPath, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to get project %s: %w", projectPath, err)
		}
		projectMap[p.ID] = p
	}

	var result []*gitlab.Project
	for _, p := range projectMap {
		result = append(result, p)
	}

	return result, nil
}

func (c *Client) listGroupProjects(ctx context.Context, groupID string) ([]*gitlab.Project, error) {
	var allProjects []*gitlab.Project
	opt := &gitlab.ListGroupProjectsOptions{
		ListOptions: gitlab.ListOptions{
			PerPage: 100,
			Page:    1,
		},
		IncludeSubGroups: gitlab.Ptr(true),
	}

	for {
		if err := c.Limiter.Wait(ctx); err != nil {
			return nil, err
		}
		projects, resp, err := c.Groups.ListGroupProjects(groupID, opt)
		if err != nil {
			return nil, err
		}
		allProjects = append(allProjects, projects...)
		if resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}
	return allProjects, nil
}

func (c *Client) listAllProjects(ctx context.Context) ([]*gitlab.Project, error) {
	var allProjects []*gitlab.Project
	opt := &gitlab.ListProjectsOptions{
		ListOptions: gitlab.ListOptions{
			PerPage: 100,
			Page:    1,
		},
	}

	for {
		if err := c.Limiter.Wait(ctx); err != nil {
			return nil, err
		}
		projects, resp, err := c.Projects.ListProjects(opt)
		if err != nil {
			return nil, err
		}
		allProjects = append(allProjects, projects...)
		if resp.NextPage == 0 {
			break
		}
		opt.Page = resp.NextPage
	}
	return allProjects, nil
}
