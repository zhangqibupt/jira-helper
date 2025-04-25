# Jira Whisperer

A powerful tool to interact with Jira through natural language commands, making Jira task management more intuitive and efficient.

## Features

- Natural language processing for Jira commands
- Seamless integration with Jira API
- Easy issue creation and management
- Quick status updates and transitions
- Efficient sprint and board management
- Support for personal Jira tokens for write operations

## Configuration

The application is configured using environment variables. Copy `.env.example` to `.env` and update the values:

### Required Environment Variables

- `SLACK_BOT_TOKEN`: Slack bot user OAuth token
- `SLACK_SIGNING_SECRET`: Slack signing secret for request verification
- `AZURE_OPENAI_KEY`: Azure OpenAI API key
- `AZURE_OPENAI_ENDPOINT`: Azure OpenAI endpoint URL
- `AZURE_OPENAI_DEPLOYMENT`: Azure OpenAI model deployment name
- `MCP_SERVER_URL`: MCP server URL
- `MCP_API_KEY`: MCP API key
- `TOKEN_BUCKET_NAME`: S3 bucket name for storing tokens

### Optional Environment Variables

- `APP_ENV`: Application environment (development, production, test). Defaults to "development"
- `LOG_LEVEL`: Logging level (debug, info, warn, error). Defaults to "info"
- `TOKEN_BUCKET_PREFIX`: Prefix for token storage in S3 bucket. Defaults to APP_ENV value

## Token Management

### Default Token
The application comes with a read-only token by default, which can be used for viewing Jira issues and basic operations.

### Personal Token
For operations that modify Jira (creating issues, updating status, etc.), you'll need to set up your personal token:

1. Generate a personal token from Jira:
   - Go to https://id.atlassian.com/manage/api-tokens
   - Click "Create API Token"
   - Give it a name and copy the token

2. Set up your token using the Slack command:
   ```
   /setup-token your-personal-token
   ```

Your personal token will be securely stored and used for all write operations in Jira.

## TODO
- [x] Implement basic workflow Jira API using uvx
- [ ] Build Docker image to deploy in AWS 
- [ ] Implement user token management, /setup-token
- [ ] Fix get history thread error
- [ ] Support DM message
- [x] Add contact info in APP description
- [ ] Support multipe repied to show thinking logic. maybe just edit the previous message.
- [ ] Use READ_ONLY_MODE to enable/disable write operations
- [ ] Remove home page for slack bot
- [ ] Fix token size issue: context length is 128000 tokens
- [ ] When displaying the issue, translate customized fields to human readable format
- [ ] Fix the tool call failed but display success message
## Usage

[Documentation to be added]

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

## License

This project is licensed under the MIT License - see the LICENSE file for details. 