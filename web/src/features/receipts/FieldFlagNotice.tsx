import { Badge } from '../../components/ui/Badge';
import { formatReviewFlagSummary, type ReviewFlag } from './flag-reasons';

interface FieldFlagNoticeProps {
  flags: readonly ReviewFlag[];
}

export function FieldFlagNotice({ flags }: FieldFlagNoticeProps) {
  if (flags.length === 0) return null;
  return (
    <div className="flex flex-col gap-1">
      {flags.map((flag) => {
        const summary = formatReviewFlagSummary(flag);
        return (
          <div
            key={flag.id}
            className="flex flex-wrap items-center gap-2 text-small text-warning"
          >
            <Badge variant="warning">{summary.label}</Badge>
            <span>{summary.reason}</span>
          </div>
        );
      })}
    </div>
  );
}
