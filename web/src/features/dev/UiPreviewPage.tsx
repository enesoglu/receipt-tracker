import { useState } from 'react';
import { Badge } from '../../components/ui/Badge';
import { Button } from '../../components/ui/Button';
import { Card } from '../../components/ui/Card';
import { Checkbox } from '../../components/ui/Checkbox';
import { DateText } from '../../components/ui/DateText';
import { Dialog } from '../../components/ui/Dialog';
import { EmptyState } from '../../components/ui/EmptyState';
import { ErrorState } from '../../components/ui/ErrorState';
import { Input } from '../../components/ui/Input';
import { Money } from '../../components/ui/Money';
import { PageHeader } from '../../components/ui/PageHeader';
import { Select } from '../../components/ui/Select';
import { Spinner } from '../../components/ui/Spinner';
import { Table, type TableColumn } from '../../components/ui/Table';
import { Tabs } from '../../components/ui/Tabs';

interface PreviewRow {
  id: string;
  store: string;
  purchasedOn: string | null;
  status: 'ready' | 'needs_review' | 'failed';
  total: string | null;
  currency: string;
}

const sampleRows: readonly PreviewRow[] = [
  {
    id: '1',
    store: 'Example Market',
    purchasedOn: '2026-11-02',
    status: 'needs_review',
    total: '129.40',
    currency: 'TRY',
  },
  {
    id: '2',
    store: 'Corner Shop',
    purchasedOn: '2026-10-18',
    status: 'ready',
    total: '44.00',
    currency: 'TRY',
  },
  {
    id: '3',
    store: 'Bakery',
    purchasedOn: null,
    status: 'failed',
    total: null,
    currency: 'EUR',
  },
];

const tableColumns: readonly TableColumn<PreviewRow>[] = [
  { key: 'store', header: 'Store', cell: (row) => row.store },
  {
    key: 'date',
    header: 'Date',
    cell: (row) => <DateText value={row.purchasedOn} />,
  },
  {
    key: 'status',
    header: 'Status',
    cell: (row) => {
      if (row.status === 'ready') return <Badge variant="success">Ready</Badge>;
      if (row.status === 'needs_review') {
        return <Badge variant="warning">Needs review</Badge>;
      }
      return <Badge variant="danger">Failed</Badge>;
    },
  },
  {
    key: 'total',
    header: 'Total',
    align: 'right',
    cell: (row) => <Money value={row.total} currency={row.currency} />,
  },
];

/** Development-only showcase of every UI primitive in every supported state. */
export function UiPreviewPage() {
  const [dialogOpen, setDialogOpen] = useState(false);
  const [checkedA, setCheckedA] = useState(true);
  const [checkedB, setCheckedB] = useState(false);
  const [selectedRow, setSelectedRow] = useState<string | null>(null);

  return (
    <main className="min-h-screen bg-canvas px-6 py-10 font-sans text-body text-ink">
      <div className="mx-auto flex max-w-shell flex-col gap-8">
        <PageHeader
          eyebrow="Development Preview"
          title="UI Primitives"
          description="Every design token and shared component rendered across its states."
          actions={
            <>
              <Button variant="secondary" onClick={() => setDialogOpen(true)}>
                Open dialog
              </Button>
              <Button>Primary action</Button>
            </>
          }
        />

        <div className="grid gap-6 lg:grid-cols-2">
          <Card title="Buttons" description="Variants, sizes, and states.">
            <div className="flex flex-col gap-4">
              <div className="flex flex-wrap items-center gap-2">
                <Button variant="primary">Primary</Button>
                <Button variant="secondary">Secondary</Button>
                <Button variant="ghost">Ghost</Button>
                <Button variant="danger">Danger</Button>
              </div>
              <div className="flex flex-wrap items-center gap-2">
                <Button size="sm">Small</Button>
                <Button variant="secondary" size="sm">
                  Small secondary
                </Button>
                <Button disabled>Disabled</Button>
                <Button loading>Loading</Button>
              </div>
            </div>
          </Card>

          <Card
            title="Badges & Spinners"
            description="Semantic pills and static loading indicators."
          >
            <div className="flex flex-col gap-4">
              <div className="flex flex-wrap items-center gap-2">
                <Badge>Neutral</Badge>
                <Badge variant="accent">Accent</Badge>
                <Badge variant="success">Ready</Badge>
                <Badge variant="warning">Needs review</Badge>
                <Badge variant="danger">Failed</Badge>
              </div>
              <div className="flex flex-wrap items-center gap-6">
                <Spinner size="sm" showLabel label="Small" />
                <Spinner size="md" showLabel label="Medium" />
                <Spinner size="lg" showLabel label="Large" />
              </div>
            </div>
          </Card>

          <Card
            title="Inputs & Selects"
            description="Default, hint, error, and disabled controls."
          >
            <div className="flex flex-col gap-4">
              <Input
                label="Store name"
                hint="Matched case-insensitively across receipts."
                defaultValue="Example Market"
              />
              <Input
                label="Line total"
                defaultValue="forty"
                error="Must be a decimal amount with two places."
              />
              <Input label="Source" value="web" disabled readOnly />
              <Select
                label="Default currency"
                defaultValue="TRY"
                hint="Applied when a receipt does not state a currency."
                options={[
                  { value: 'TRY', label: 'Turkish Lira (TRY)' },
                  { value: 'EUR', label: 'Euro (EUR)' },
                  { value: 'USD', label: 'US Dollar (USD)' },
                ]}
              />
              <Select
                label="Category"
                placeholder="Select category"
                defaultValue=""
                error="Please select a category."
                options={[
                  { value: 'dairy', label: 'Dairy' },
                  { value: 'bakery', label: 'Bakery' },
                ]}
              />
              <Select
                label="Extraction provider"
                disabled
                defaultValue="azure"
                options={[{ value: 'azure', label: 'Azure Document Intel' }]}
              />
            </div>
          </Card>

          <Card
            title="Checkboxes, Money & Dates"
            description="Selection controls and deterministic value formatting."
          >
            <div className="flex flex-col gap-4">
              <Checkbox
                label="Remember store alias"
                description="Auto-link this name at Example Market next time."
                checked={checkedA}
                onChange={(event) => setCheckedA(event.target.checked)}
              />
              <Checkbox
                label="Keep untouched original"
                checked={checkedB}
                onChange={(event) => setCheckedB(event.target.checked)}
              />
              <Checkbox
                label="Confirm mismatch"
                error="Confirm before continuing."
              />
              <Checkbox label="Locked setting" checked disabled readOnly />

              <div className="grid grid-cols-2 gap-3 border-t border-border pt-4 text-small">
                <span className="text-muted">Positive TRY</span>
                <Money value="84.50" currency="TRY" />
                <span className="text-muted">Discount EUR</span>
                <Money value="-5.00" currency="EUR" />
                <span className="text-muted">Missing amount</span>
                <Money value={null} currency="TRY" />
                <span className="text-muted">Calendar date</span>
                <DateText value="2026-11-02" />
                <span className="text-muted">Month</span>
                <DateText value="2026-11" format="month" />
                <span className="text-muted">UTC timestamp</span>
                <DateText value="2026-11-02T16:05:12Z" format="datetime" />
              </div>
            </div>
          </Card>
        </div>

        <Card title="Tabs" description="Keyboard-accessible tabbed panels.">
          <Tabs
            label="Preview tabs"
            items={[
              {
                id: 'overview',
                label: 'Overview',
                badge: 3,
                content: (
                  <p className="text-small text-muted">
                    Use Left/Right Arrow, Home, and End keys to move between
                    tabs.
                  </p>
                ),
              },
              {
                id: 'flags',
                label: 'Flags',
                badge: 2,
                content: (
                  <p className="text-small text-muted">
                    Two open review flags waiting for confirmation.
                  </p>
                ),
              },
              {
                id: 'locked',
                label: 'Disabled',
                disabled: true,
                content: <p>Unavailable</p>,
              },
            ]}
          />
        </Card>

        <div className="flex flex-col gap-4">
          <div className="flex items-center justify-between">
            <h2 className="text-heading font-semibold text-ink">Tables</h2>
            {selectedRow ? (
              <p className="text-small text-muted" role="status">
                Selected row: {selectedRow}
              </p>
            ) : null}
          </div>
          <Table
            caption="Sample receipts"
            columns={tableColumns}
            rows={sampleRows}
            rowKey={(row) => row.id}
            onRowClick={(row) => setSelectedRow(row.store)}
            getRowLabel={(row) => `Select ${row.store}`}
          />
          <Table
            caption="Empty sample table"
            columns={tableColumns}
            rows={[]}
            rowKey={(row) => row.id}
            emptyMessage="No receipts match the current filter."
          />
        </div>

        <div className="grid gap-6 lg:grid-cols-2">
          <EmptyState
            title="No receipts yet"
            description="Upload a JPEG, PNG, or HEIC photo to start tracking prices."
            action={<Button>Upload receipt</Button>}
          />
          <div className="flex flex-col gap-4">
            <ErrorState
              title="Storage unavailable"
              message="Image storage needs to be reconnected by an administrator."
              onRetry={() => undefined}
            />
            <ErrorState message="Could not reach the server." />
          </div>
        </div>
      </div>

      <Dialog
        open={dialogOpen}
        onClose={() => setDialogOpen(false)}
        title="Confirm action"
        description="Press Escape or click outside to dismiss this modal."
        footer={
          <>
            <Button variant="secondary" onClick={() => setDialogOpen(false)}>
              Cancel
            </Button>
            <Button onClick={() => setDialogOpen(false)}>Save</Button>
          </>
        }
      >
        <Input label="Canonical name" defaultValue="Ayran 1 l" />
      </Dialog>
    </main>
  );
}
