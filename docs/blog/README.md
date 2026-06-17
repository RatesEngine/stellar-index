# Blog posts

Markdown files rendered at `stellarindex.io/blog`. Naming convention:

```
docs/blog/<YYYY-MM-DD>-<slug>.md
```

Frontmatter:

```markdown
---
title: My post title
date: 2026-05-07
author: Operator
summary: One-line description that appears on the /blog index card.
---

<body markdown>
```

The loader at `web/explorer/src/lib/blog.ts` reads this directory at
build time. Posts are sorted newest-first by the date prefix in the
filename.
