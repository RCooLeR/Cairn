import { Database, RefreshCw } from "lucide-react";
import { useState } from "react";

import { APP_ERROR_CODES, appErrorPresentation } from "../../api/errors";
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
} from ".";

type Row = {
  id: string;
  name: string;
  state: string;
};

export const AppErrorMatrix = () => (
  <div className="min-h-screen bg-bg-app p-6 text-text-primary">
    <div className="mb-4">
      <h1 className="text-xl font-semibold">AppError Matrix</h1>
      <p className="mt-1 text-sm text-text-muted">
        Contract error codes mapped to their required UI surface.
      </p>
    </div>
    <div className="grid gap-3">
      {APP_ERROR_CODES.map((code) => {
        const item = appErrorPresentation(code);
        return (
          <Card key={code}>
            <CardBody>
              <div className="flex flex-wrap items-start justify-between gap-3">
                <div className="min-w-0">
                  <div className="font-mono text-xs text-text-muted">
                    {item.code}
                  </div>
                  <div className="mt-1 font-medium text-text-primary">
                    {item.title}
                  </div>
                  <div className="mt-1 text-sm text-text-secondary">
                    {item.body}
                  </div>
                </div>
                <div className="flex shrink-0 flex-wrap gap-2">
                  <Badge tone={item.tone}>{item.tone}</Badge>
                  <Badge tone="info">{item.surface}</Badge>
                  <Badge tone="neutral">{item.action}</Badge>
                </div>
              </div>
            </CardBody>
          </Card>
        );
      })}
    </div>
  </div>
);

const rows: Row[] = [
  { id: "api", name: "api", state: "running" },
  { id: "worker", name: "worker", state: "stopped" },
];

export const Components = () => {
  const [tab, setTab] = useState("overview");
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
        <CardHeader
          status={<StatusDot label="Running" tone="ok" />}
          title="Provider Health"
        />
        <CardBody>
          <Tabs
            activeID={tab}
            items={[
              { id: "overview", label: "Overview" },
              { id: "services", label: "Services" },
            ]}
            onChange={setTab}
          >
            <div className="pt-4 text-sm text-text-secondary">
              Active tab: {tab}
            </div>
          </Tabs>
        </CardBody>
      </Card>

      <DataTable
        bulkActions={<Button size="sm">Stop</Button>}
        columns={[
          {
            id: "name",
            header: "Name",
            render: (row) => row.name,
            sortable: true,
          },
          { id: "state", header: "State", render: (row) => row.state },
        ]}
        getRowID={(row) => row.id}
        onToggleAllRows={(ids, isSelected) =>
          setSelected((current) => {
            const next = new Set(current);
            for (const id of ids) {
              if (isSelected) {
                next.add(id);
              } else {
                next.delete(id);
              }
            }
            return next;
          })
        }
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

      <Toast
        body="The Docker provider is currently unavailable."
        level="warn"
        title="Provider degraded"
      />

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
