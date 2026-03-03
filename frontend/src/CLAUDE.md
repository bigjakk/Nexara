# frontend/src — Component & Feature Conventions

## Component Rules
- Functional components with TypeScript — no class components
- Shadcn/ui for all UI primitives (Button, Dialog, Table, etc.)
- **Never use raw `fetch` in components** — use TanStack Query hooks
- Zustand for client-only state; TanStack Query for server state
- Tailwind CSS utility classes for styling

## Feature Module Structure
```
features/<name>/
  pages/        — route-level page components
  components/   — feature-specific UI components
  hooks/        — custom hooks (data fetching, logic)
  api/          — TanStack Query hooks & API functions
  types/        — TypeScript types/interfaces for this feature
```

## Rules
- Strict TypeScript — no `any` types
- Co-locate tests next to source files (`Component.test.tsx`)
- Shared components go in `components/ui/` (Shadcn) or `components/`
- Icons: use Lucide React exclusively
