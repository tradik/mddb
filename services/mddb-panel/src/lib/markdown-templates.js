/**
 * Markdown Templates
 * Pre-defined templates for common document types
 */

export const templates = {
  blog: `# Blog Post Title

**Published:** ${new Date().toLocaleDateString()}  
**Author:** Your Name  
**Tags:** #tag1 #tag2

## Introduction

Write your introduction here. Grab the reader's attention and explain what this post is about.

## Main Content

### Section 1

Your content here...

### Section 2

More content...

## Key Takeaways

- Point 1
- Point 2
- Point 3

## Conclusion

Wrap up your post with a conclusion and call to action.

---

*Thank you for reading!*
`,

  documentation: `# Documentation Title

## Overview

Brief overview of what this documentation covers.

## Table of Contents

- [Getting Started](#getting-started)
- [Installation](#installation)
- [Usage](#usage)
- [API Reference](#api-reference)
- [Examples](#examples)
- [FAQ](#faq)

## Getting Started

### Prerequisites

- Requirement 1
- Requirement 2
- Requirement 3

### Installation

\`\`\`bash
# Installation command
npm install package-name
\`\`\`

## Usage

### Basic Usage

\`\`\`javascript
// Code example
const example = require('package-name');
example.doSomething();
\`\`\`

### Advanced Usage

More detailed examples...

## API Reference

### Method Name

**Description:** What this method does

**Parameters:**
- \`param1\` (string) - Description
- \`param2\` (number) - Description

**Returns:** Return type and description

**Example:**
\`\`\`javascript
example.methodName('value', 123);
\`\`\`

## Examples

### Example 1: Basic Example

\`\`\`javascript
// Example code
\`\`\`

### Example 2: Advanced Example

\`\`\`javascript
// More complex example
\`\`\`

## FAQ

### Question 1?

Answer to question 1.

### Question 2?

Answer to question 2.

## Contributing

Contributions are welcome! Please read our contributing guidelines.

## License

MIT License - see LICENSE file for details.
`,

  readme: `# Project Name

[![License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![Version](https://img.shields.io/badge/version-1.0.0-green.svg)](https://github.com/user/repo)

**Brief description of your project**

## Features

- ✅ Feature 1
- ✅ Feature 2
- ✅ Feature 3
- ✅ Feature 4

## Quick Start

### Installation

\`\`\`bash
npm install your-package
\`\`\`

### Usage

\`\`\`javascript
const package = require('your-package');

// Basic usage
package.doSomething();
\`\`\`

## Documentation

For full documentation, visit [docs link](https://docs.example.com)

## Examples

### Example 1

\`\`\`javascript
// Example code
\`\`\`

### Example 2

\`\`\`javascript
// Another example
\`\`\`

## API

### \`methodName(param1, param2)\`

Description of what this method does.

**Parameters:**
- \`param1\` (type) - Description
- \`param2\` (type) - Description

**Returns:** Description of return value

## Contributing

1. Fork the repository
2. Create your feature branch (\`git checkout -b feature/amazing-feature\`)
3. Commit your changes (\`git commit -m 'Add amazing feature'\`)
4. Push to the branch (\`git push origin feature/amazing-feature\`)
5. Open a Pull Request

## License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.

## Contact

- **Author:** Your Name
- **Email:** your.email@example.com
- **GitHub:** [@yourusername](https://github.com/yourusername)

## Acknowledgments

- Thanks to contributor 1
- Thanks to contributor 2
`,

  api: `# API Documentation

## Overview

This API provides access to [describe your API].

**Base URL:** \`https://api.example.com/v1\`

## Authentication

All API requests require authentication using an API key:

\`\`\`bash
curl -H "Authorization: Bearer YOUR_API_KEY" https://api.example.com/v1/endpoint
\`\`\`

## Endpoints

### GET /resource

Get a list of resources.

**Parameters:**
- \`limit\` (number, optional) - Maximum number of results (default: 10)
- \`offset\` (number, optional) - Offset for pagination (default: 0)

**Response:**
\`\`\`json
{
  "data": [
    {
      "id": "123",
      "name": "Resource Name",
      "created_at": "2024-01-01T00:00:00Z"
    }
  ],
  "total": 100,
  "limit": 10,
  "offset": 0
}
\`\`\`

### GET /resource/:id

Get a specific resource by ID.

**Parameters:**
- \`id\` (string, required) - Resource ID

**Response:**
\`\`\`json
{
  "id": "123",
  "name": "Resource Name",
  "created_at": "2024-01-01T00:00:00Z"
}
\`\`\`

### POST /resource

Create a new resource.

**Request Body:**
\`\`\`json
{
  "name": "New Resource",
  "description": "Description here"
}
\`\`\`

**Response:**
\`\`\`json
{
  "id": "124",
  "name": "New Resource",
  "created_at": "2024-01-01T00:00:00Z"
}
\`\`\`

### PUT /resource/:id

Update an existing resource.

**Parameters:**
- \`id\` (string, required) - Resource ID

**Request Body:**
\`\`\`json
{
  "name": "Updated Name"
}
\`\`\`

**Response:**
\`\`\`json
{
  "id": "123",
  "name": "Updated Name",
  "updated_at": "2024-01-01T00:00:00Z"
}
\`\`\`

### DELETE /resource/:id

Delete a resource.

**Parameters:**
- \`id\` (string, required) - Resource ID

**Response:**
\`\`\`json
{
  "success": true,
  "message": "Resource deleted"
}
\`\`\`

## Error Codes

| Code | Description |
|------|-------------|
| 200  | Success |
| 201  | Created |
| 400  | Bad Request |
| 401  | Unauthorized |
| 404  | Not Found |
| 500  | Internal Server Error |

## Rate Limiting

API requests are limited to 1000 requests per hour per API key.

## Examples

### cURL

\`\`\`bash
curl -X GET "https://api.example.com/v1/resource" \\
  -H "Authorization: Bearer YOUR_API_KEY"
\`\`\`

### JavaScript

\`\`\`javascript
fetch('https://api.example.com/v1/resource', {
  headers: {
    'Authorization': 'Bearer YOUR_API_KEY'
  }
})
.then(response => response.json())
.then(data => console.log(data));
\`\`\`

### Python

\`\`\`python
import requests

headers = {'Authorization': 'Bearer YOUR_API_KEY'}
response = requests.get('https://api.example.com/v1/resource', headers=headers)
data = response.json()
\`\`\`

## Support

For support, email support@example.com or visit our [documentation](https://docs.example.com).
`,

  changelog: `# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- New feature description

### Changed
- Changed feature description

### Fixed
- Bug fix description

## [1.0.0] - ${new Date().toISOString().split('T')[0]}

### Added
- Initial release
- Feature 1
- Feature 2
- Feature 3

### Changed
- Improvement 1
- Improvement 2

### Deprecated
- Old feature that will be removed

### Removed
- Removed feature

### Fixed
- Bug fix 1
- Bug fix 2

### Security
- Security improvement

## [0.9.0] - 2024-01-01

### Added
- Beta feature 1
- Beta feature 2

### Fixed
- Beta bug fix

## [0.1.0] - 2023-12-01

### Added
- Alpha release
- Basic functionality

---

## Types of Changes

- \`Added\` for new features
- \`Changed\` for changes in existing functionality
- \`Deprecated\` for soon-to-be removed features
- \`Removed\` for now removed features
- \`Fixed\` for any bug fixes
- \`Security\` in case of vulnerabilities
`
};

export function getTemplate(type) {
  return templates[type] || '';
}

export function getTemplateList() {
  return Object.keys(templates);
}
