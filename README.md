## What will this do?

- Fetch open PRs from a GitHub repository.
- Filter PRs by authorship, excluding members of specified GitHub organizations.
- Exclude PRs authored by bots by default, with an option to include them.
- Command-line options to specify repository details and filtering preferences.
- Sorted output with the most recent PRs listed last.

## Prerequisites

- A GitHub Personal Access Token (PAT) with appropriate permissions to read repository data.
- The `GITHUB_TOKEN` environment variable must be set with your GitHub PAT.

## Usage

### Command-Line Options

- `-owner`: Repository owner (default: `rancher`)
- `-repo`: Repository name (default: `rancher`)
- `-orgs`: Comma-separated list of GitHub organizations to check membership against (default: `rancher,SUSE`)
- `-includebots`: Include PRs authored by bots (default: `false`)

### Output

The output will list PRs created by users who are not members of the specified organizations, sorted by creation date with the most recent PRs at the end. Each PR will display:

- PR number
- Author's GitHub username
- PR title
- Link to the PR


