import { useNavigate } from 'react-router';
import { Button } from '../../components/ui/Button';
import { EmptyState } from '../../components/ui/EmptyState';
import { PageHeader } from '../../components/ui/PageHeader';

export interface PlaceholderPageProps {
  eyebrow?: string;
  title: string;
  description: string;
  emptyTitle: string;
  emptyDescription: string;
  actionLabel?: string;
  actionTo?: string;
}

/** Standard placeholder screen combining PageHeader and EmptyState for upcoming routes. */
export function PlaceholderPage({
  eyebrow,
  title,
  description,
  emptyTitle,
  emptyDescription,
  actionLabel,
  actionTo,
}: PlaceholderPageProps) {
  const navigate = useNavigate();

  return (
    <div className="flex flex-col gap-6">
      <PageHeader eyebrow={eyebrow} title={title} description={description} />
      <EmptyState
        title={emptyTitle}
        description={emptyDescription}
        action={
          actionLabel && actionTo ? (
            <Button
              variant="secondary"
              onClick={() => {
                void navigate(actionTo);
              }}
            >
              {actionLabel}
            </Button>
          ) : undefined
        }
      />
    </div>
  );
}
