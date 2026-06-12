import { Database, RefreshCw } from 'lucide-react';
import { useState } from 'react';

import {
  Badge,
  Button,
  Card,
  CardBody,
  CardHeader,
  DataTable,
  EmptyState,
  Modal,
  StatusDot,
  Tabs,
  Toast,
} from '.';

type Row = {
  id: string;
  name: string;
  state: string;
};

const rows: Row[] = [
  { id: 'api', name: 'api', state: 'running' },
  { id: 'worker', name: 'worker', state: 'stopped' },
];

export const Components = () => {
  const [tab, setTab] = useState('overview');
  const [open, setOpen] = useState(false);
  const [selected, setSelected] = useState(new Set<string>());

  return (
    <div className="min-h-screen space-y-4 bg-bg-app p-6 text-text-primary">
      <div className="flex flex-wrap gap-2">
        <Button icon={<RefreshCw size={15} />} variant="primary">
          Refresh
        </Button>
        <Button variant="secondary">Secondary</Button>
        <Button variant="ghost">Ghost</Button>
        <Button variant="danger">Delete</Button>
        <Badge tone="ok">Up to date</Badge>
        <Badge pulse tone="warn">
          Checking
        </Badge>
      </div>

      <Card>
        <CardHeader status={<StatusDot label="Running" tone="ok" />} title="Provider Health" />
        <CardBody>
          <Tabs
            activeID={tab}
            items={[
              { id: 'overview', label: 'Overview' },
              { id: 'services', label: 'Services' },
            ]}
            onChange={setTab}
          >
            <div className="pt-4 text-sm text-text-secondary">Active tab: {tab}</div>
          </Tabs>
        </CardBody>
      </Card>

      <DataTable
        bulkActions={<Button size="sm">Stop</Button>}
        columns={[
          { id: 'name', header: 'Name', render: (row) => row.name, sortable: true },
          { id: 'state', header: 'State', render: (row) => row.state },
        ]}
        getRowID={(row) => row.id}
        onToggleRow={(id) =>
          setSelected((current) => {
            const next = new Set(current);
            if (next.has(id)) {
              next.delete(id);
            } else {
              next.add(id);
            }
            return next;
          })
        }
        rows={rows}
        selectedIDs={selected}
      />

      <EmptyState
        action={<Button onClick={() => setOpen(true)}>Open modal</Button>}
        body="Connect a provider to populate Docker objects."
        icon={<Database size={24} />}
        title="No provider configured"
      />

      <Toast body="The Docker provider is currently unavailable." level="warn" title="Provider degraded" />

      <Modal
        footer={<Button variant="danger">Confirm</Button>}
        onClose={() => setOpen(false)}
        open={open}
        title="Danger Action"
      >
        This modal uses the danger variant for destructive confirmations.
      </Modal>
    </div>
  );
};
