package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/google/go-github/v55/github"
	"golang.org/x/oauth2"
)

func main() {
	owner := flag.String("owner", "rancher", "Repository owner")
	repo := flag.String("repo", "rancher", "Repository name")
	orgs := flag.String("orgs", "rancher,SUSE", "Comma-separated list of organizations")
	includeBots := flag.Bool("includebots", false, "Include PRs authored by bots")

	flag.Parse()

	ctx := context.Background()
	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: os.Getenv("GITHUB_TOKEN")},
	)
	tc := oauth2.NewClient(ctx, ts)
	client := github.NewClient(tc)

	orgList := strings.Split(*orgs, ",")

	members := make(map[string]bool)

	for _, org := range orgList {
		opt := &github.ListMembersOptions{
			ListOptions: github.ListOptions{
				PerPage: 100,
			},
		}

		for {
			orgMembers, resp, err := client.Organizations.ListMembers(ctx, org, opt)
			if err != nil {
				log.Fatalf("Error fetching members from %s organization: %v", org, err)
			}

			for _, member := range orgMembers {
				members[member.GetLogin()] = true
			}

			log.Printf("Fetched %d members from %s (total members so far: %d)", len(orgMembers), org, len(members))

			if resp.NextPage == 0 {
				break
			}

			opt.Page = resp.NextPage

			// try to play nicely
			time.Sleep(time.Second * 1)
		}
	}

	prOpt := &github.PullRequestListOptions{
		State: "open",
		ListOptions: github.ListOptions{
			PerPage: 100,
		},
	}

	var allPRs []*github.PullRequest
	for {
		prs, resp, err := client.PullRequests.List(ctx, *owner, *repo, prOpt)
		if err != nil {
			log.Fatalf("Error fetching PRs: %v", err)
		}
		allPRs = append(allPRs, prs...)
		log.Printf("Fetched %d PRs (total so far: %d)", len(prs), len(allPRs))

		if resp.NextPage == 0 {
			break
		}

		prOpt.Page = resp.NextPage
	}

	sort.Slice(allPRs, func(i, j int) bool {
		return allPRs[i].GetCreatedAt().After(allPRs[j].GetCreatedAt().Time)
	})

	fmt.Printf("PRs created by users outside of %s and SUSE:\n", orgList)
	fmt.Println("-------------------------------------------")
	for _, pr := range allPRs {
		author := pr.User.GetLogin()

		if _, isMember := members[author]; !isMember {
			if !*includeBots && strings.Contains(author, "[bot]") {
				continue
			}
			fmt.Printf("PR #%d by %s\nTitle: %s\nLink: %s\n\n", pr.GetNumber(), author, pr.GetTitle(), pr.GetHTMLURL())
		}
	}
}
