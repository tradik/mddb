# GitHub Pages Setup

This document explains how to enable GitHub Pages for the MDDB website.

## Enable GitHub Pages

1. Go to your GitHub repository: https://github.com/tradik/mddb
2. Click on **Settings**
3. Scroll down to **Pages** section (in the left sidebar)
4. Under **Source**, select:
   - **Branch**: `main`
   - **Folder**: `/docs`
5. Click **Save**

GitHub will automatically deploy the website from the `docs` folder.

## Access the Website

After enabling GitHub Pages, the website will be available at:
- **https://tradik.github.io/mddb/**

It may take a few minutes for the first deployment.

## Files

The website consists of:
- `docs/index.html` - Main website page
- `docs/styles.css` - Stylesheet
- `docs/swagger.html` - Swagger UI (already exists)
- `docs/openapi.yaml` - OpenAPI specification (already exists)
- Other documentation files (*.md)

## Features

The website includes:
- ✅ Modern, responsive design
- ✅ SEO optimized (meta tags, Open Graph, Twitter Cards)
- ✅ Mobile-friendly navigation
- ✅ Interactive tabs and examples
- ✅ Direct links to downloads and documentation
- ✅ Smooth scrolling and animations
- ✅ GitHub and Docker Hub integration

## Custom Domain (Optional)

To use a custom domain:

1. Add a `CNAME` file in the `docs` folder with your domain:
   ```
   mddb.example.com
   ```

2. Configure DNS records at your domain provider:
   - Add a CNAME record pointing to `tradik.github.io`

3. In GitHub Pages settings, add your custom domain

## Updating the Website

Any changes pushed to `docs/index.html` or `docs/styles.css` in the `main` branch will automatically update the website.

## Testing Locally

To test the website locally:

```bash
# Using Python
cd docs
python3 -m http.server 8000

# Visit http://localhost:8000
```

Or use any static file server of your choice.
