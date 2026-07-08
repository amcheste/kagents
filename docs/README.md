# Docs site

This directory holds the [mkdocs-material](https://squidfunk.github.io/mkdocs-material/) source for [kagents.dev](https://kagents.dev).

## Local development

```bash
pip install -r docs/requirements.txt
mkdocs serve  # http://localhost:8000
```

Edits to any file under `docs/` or `mkdocs.yml` hot-reload in the browser.

## Deploying

A push to `main` that touches `docs/`, `mkdocs.yml`, or `.github/workflows/docs.yml` triggers `Deploy Docs`, which builds the site with `mkdocs gh-deploy` and force-pushes the rendered HTML to the `gh-pages` branch. GitHub Pages serves it at https://kagents.dev (and at `amcheste.github.io/kagents` until the custom domain DNS resolves).

## Structure

The site uses the [Diátaxis framework](https://diataxis.fr). Four sections: Tutorials, How-to guides, Reference, Explanation. Section pages will be filled in by the v0.7.0 content issues. For now only the homepage exists.
