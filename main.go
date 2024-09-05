package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/machinebox/graphql"
	"golang.org/x/oauth2"
)

type Member struct {
	Login string `json:"login"`
}

func main() {
	owner := flag.String("owner", "rancher", "Repository owner")
	repo := flag.String("repo", "rancher", "Repository name")
	orgs := flag.String("orgs", "rancher,SUSE", "Comma-separated list of organizations")
	includeBots := flag.Bool("includebots", false, "Include PRs authored by bots")
	botsToExclude := flag.String("botstoexclude", "", "Comma-separated list of bots to exclude")
	addToProject := flag.Bool("addtoproject", false, "Add matching PRs to the given project")
	projectNumber := flag.Int("project", 79, "GitHub project number")

	flag.Parse()
	ctx := context.Background()

	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		log.Fatal("GITHUB_TOKEN is required")
	}

	var httpClient = oauth2.NewClient(ctx, oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: token},
	))
	httpClient.Timeout = 15 * time.Second
	client := graphql.NewClient("https://api.github.com/graphql", graphql.WithHTTPClient(httpClient))

	orgList := strings.Split(*orgs, ",")
	botsToExcludeList := strings.Split(*botsToExclude, ",")

	// Get project global ID
	projectGlobalID, err := getProjectV2ID(ctx, client, *owner, *projectNumber)
	if err != nil {
		log.Fatalf("Failed to fetch project ID: %v", err)
	}

	// Fetch organization members
	members := make(map[string]bool)
	for _, org := range orgList {
		err := fetchOrgMembers(ctx, token, org, members)
		if err != nil {
			log.Fatalf("Error fetching members from %s organization: %v", org, err)
		}
		log.Printf("Fetched members from org %s.  Total members list is now: %d", org, len(members))
	}

	// Fetch pull requests
	cursor := ""
	var pullRequests []struct {
		Number    int
		Title     string
		URL       string
		CreatedAt time.Time
		Author    string
	}

	for {
		req := graphql.NewRequest(`
			query ($owner: String!, $repo: String!, $cursor: String) {
				repository(owner: $owner, name: $repo) {
					pullRequests(first: 100, after: $cursor, states: OPEN) {
						nodes {
							number
							title
							url
							createdAt
							author {
								login
							}
						}
						pageInfo {
							endCursor
							hasNextPage
						}
					}
				}
			}
		`)
		req.Var("owner", *owner)
		req.Var("repo", *repo)
		req.Var("cursor", cursor)

		var resp struct {
			Repository struct {
				PullRequests struct {
					Nodes []struct {
						Number    int
						Title     string
						URL       string
						CreatedAt string
						Author    struct {
							Login string
						}
					}
					PageInfo struct {
						EndCursor   string
						HasNextPage bool
					}
				}
			}
		}

		if err := client.Run(ctx, req, &resp); err != nil {
			log.Fatalf("Error fetching PRs: %v", err)
		}

		for _, pr := range resp.Repository.PullRequests.Nodes {
			pullRequests = append(pullRequests, struct {
				Number    int
				Title     string
				URL       string
				CreatedAt time.Time
				Author    string
			}{
				Number:    pr.Number,
				Title:     pr.Title,
				URL:       pr.URL,
				CreatedAt: parseTime(pr.CreatedAt),
				Author:    pr.Author.Login,
			})
		}

		if !resp.Repository.PullRequests.PageInfo.HasNextPage {
			break
		}
		cursor = resp.Repository.PullRequests.PageInfo.EndCursor
	}

	sort.Slice(pullRequests, func(i, j int) bool {
		return pullRequests[i].CreatedAt.Before(pullRequests[j].CreatedAt)
	})

	fmt.Printf("PRs created by users outside of %s:\n", orgList)
	fmt.Printf("-------------------------------------------")
	for _, pr := range pullRequests {
		if _, isMember := members[pr.Author]; !isMember {
			if !*includeBots && slices.Contains(botsToExcludeList, pr.Author) {
				continue
			}
			fmt.Printf("\nPR #%d by %s\nTitle: %s\nLink: %s\n", pr.Number, pr.Author, pr.Title, pr.URL)

			if *addToProject {
				added, err := addPRToProject(ctx, client, projectGlobalID, *owner, *repo, pr.Number)
				if err != nil {
					log.Printf("Error adding PR #%d to project: %v", pr.Number, err)
				}
				if added {
					fmt.Printf("PR #%d added to project %v\n", pr.Number, *projectNumber)
				} else {
					fmt.Printf("PR #%d already in project %v\n", pr.Number, *projectNumber)
				}
			}
		}
	}
}

// parseTime parses the GitHub date-time format into time.Time
func parseTime(dateTime string) time.Time {
	t, err := time.Parse(time.RFC3339, dateTime)
	if err != nil {
		log.Fatalf("Error parsing date-time: %v", err)
	}
	return t
}

// getProjectV2ID fetches the global ID for the ProjectV2
func getProjectV2ID(ctx context.Context, client *graphql.Client, org string, projectNumber int) (string, error) {
	req := graphql.NewRequest(`
		query($org: String!, $projectNumber: Int!) {
			organization(login: $org) {
				projectV2(number: $projectNumber) {
					id
				}
			}
		}
	`)
	req.Var("org", org)
	req.Var("projectNumber", projectNumber)

	var resp struct {
		Organization struct {
			ProjectV2 struct {
				ID string `json:"id"`
			} `json:"projectV2"`
		} `json:"organization"`
	}

	if err := client.Run(ctx, req, &resp); err != nil {
		return "", fmt.Errorf("error fetching project ID: %w", err)
	}

	return resp.Organization.ProjectV2.ID, nil
}

// addPRToProject fetches the global ID of the PR and adds it to the specified project using the global ID
func addPRToProject(ctx context.Context, client *graphql.Client, projectID string, owner string, repo string, prNumber int) (bool, error) {
	// Fetch the global ID of the PR
	prID, err := getPullRequestID(ctx, client, owner, repo, prNumber)
	if err != nil {
		return false, fmt.Errorf("error fetching global ID for PR #%d: %w", prNumber, err)
	}

	// Check if the PR is already in the project
	isInProject, err := checkPRInProject(ctx, client, projectID, prID)
	if err != nil {
		return false, fmt.Errorf("error checking PR in project: %w", err)
	}

	if isInProject {
		return false, nil
	}

	// Add PR to the project using the fetched PR global ID
	req := graphql.NewRequest(`
		mutation($projectID: ID!, $prID: ID!) {
			addProjectV2ItemById(input: {projectId: $projectID, contentId: $prID}) {
				item {
					id
				}
			}
		}
	`)

	req.Var("projectID", projectID)
	req.Var("prID", prID)

	var mutationResp struct {
		AddProjectV2ItemById struct {
			Item struct {
				ID string `json:"id"`
			} `json:"item"`
		} `json:"addProjectV2ItemById"`
	}

	if err := client.Run(ctx, req, &mutationResp); err != nil {
		return false, fmt.Errorf("error adding PR to project: %w", err)
	}

	return true, nil
}

// getPullRequestID fetches the global ID for a given PR by its number
func getPullRequestID(ctx context.Context, client *graphql.Client, owner string, repo string, prNumber int) (string, error) {
	req := graphql.NewRequest(`
		query($owner: String!, $repo: String!, $prNumber: Int!) {
			repository(owner: $owner, name: $repo) {
				pullRequest(number: $prNumber) {
					id
				}
			}
		}
	`)

	req.Var("owner", owner)
	req.Var("repo", repo)
	req.Var("prNumber", prNumber)

	var resp struct {
		Repository struct {
			PullRequest struct {
				ID string `json:"id"`
			} `json:"pullRequest"`
		} `json:"repository"`
	}

	if err := client.Run(ctx, req, &resp); err != nil {
		return "", fmt.Errorf("error fetching PR ID: %w", err)
	}

	return resp.Repository.PullRequest.ID, nil
}

// fetchOrgMembers fetches all members from a GitHub organization using the REST API
// This is using the REST API instead of graphql because we need ALL org members and MembersWithRole
// doesn't give us the full list that we need.
func fetchOrgMembers(ctx context.Context, token, org string, members map[string]bool) error {
	client := &http.Client{
		Timeout: time.Second * 15,
	}

	perPage := 100
	page := 1

	for {
		req, err := http.NewRequest("GET", fmt.Sprintf("https://api.github.com/orgs/%s/members?per_page=%d&page=%d", org, perPage, page), nil)
		if err != nil {
			return fmt.Errorf("error creating request: %v", err)
		}

		req.Header.Set("Authorization", "token "+token)

		//log.Printf("Making call to fetch 100 members for %s", org)
		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("error making request: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("error: received non-OK response %d", resp.StatusCode)
		}

		var orgMembers []Member
		if err := json.NewDecoder(resp.Body).Decode(&orgMembers); err != nil {
			return fmt.Errorf("error decoding response: %v", err)
		}

		for _, member := range orgMembers {
			members[member.Login] = true
		}

		if len(orgMembers) < perPage {
			break
		}
		page++
	}

	return nil
}

// checkPRInProject checks if a pull request is already in the specified project.
func checkPRInProject(ctx context.Context, client *graphql.Client, projectID, prID string) (bool, error) {
	req := graphql.NewRequest(`
		query($projectID: ID!) {
			node(id: $projectID) {
				... on ProjectV2 {
					items(first: 100) {
						nodes {
							id
							content {
								... on PullRequest {
									id
								}
							}
						}
					}
				}
			}
		}
	`)

	req.Var("projectID", projectID)

	var resp struct {
		Node struct {
			Items struct {
				Nodes []struct {
					ID      string
					Content struct {
						ID string
					}
				}
			}
		}
	}

	if err := client.Run(ctx, req, &resp); err != nil {
		return false, fmt.Errorf("error checking PR in project: %w", err)
	}

	for _, item := range resp.Node.Items.Nodes {
		if item.Content.ID == prID {
			return true, nil
		}
	}

	return false, nil
}
