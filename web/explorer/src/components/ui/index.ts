// Stellar Index design-system component library.
// The single import surface for UI primitives: `import { Card, Button, … } from '@/components/ui'`.
// Tokens live in tailwind.config.ts; guide at /dev/primitives + docs/architecture/design-system.md.

export { Button, ButtonLink, buttonClass } from './Button';
export type { ButtonVariant, ButtonSize } from './Button';
export { Card, CardHeader, CardBody, CardFooter } from './Card';
export { Badge } from './Badge';
export type { BadgeTone } from './Badge';
export { Stat, StatGrid, StatCell } from './Stat';
export { TableWrap, Table, THead, TBody, TR, Th, Td } from './Table';
export { Container, Section, PageHeader, Breadcrumbs, SectionHeader } from './Page';
export type { Crumb } from './Page';
export { Input, Textarea, Select, Field } from './Input';
export { EmptyState, Skeleton, Callout } from './Feedback';
export { TabNav, Segmented } from './Tabs';
export type { TabItem } from './Tabs';
export { Mono, CopyButton, truncateMiddle } from './Mono';
