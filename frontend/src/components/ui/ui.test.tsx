import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import { Button, DataTable, Modal, StatusDot, Tabs } from '.';

describe('UI kit', () => {
  it('renders button loading and disabled states', () => {
    render(
      <Button disabledReason="Waiting for provider" loading>
        Start
      </Button>,
    );

    expect(screen.getByRole('button', { name: 'Start' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Start' })).toHaveAttribute('title', 'Waiting for provider');
  });

  it('renders status text without relying on color alone', () => {
    render(<StatusDot label="Running" tone="ok" />);

    expect(screen.getByText('Running')).toBeInTheDocument();
  });

  it('switches tabs with buttons', async () => {
    const onChange = vi.fn();

    render(
      <Tabs
        activeID="overview"
        items={[
          { id: 'overview', label: 'Overview' },
          { id: 'services', label: 'Services' },
        ]}
        onChange={onChange}
      >
        Content
      </Tabs>,
    );

    fireEvent.click(screen.getByRole('tab', { name: 'Services' }));
    expect(onChange).toHaveBeenCalledWith('services');

    fireEvent.keyDown(screen.getByRole('tablist'), { key: 'ArrowRight' });
    expect(onChange).toHaveBeenCalledWith('services');
  });

  it('sorts table rows by sortable columns', async () => {
    render(
      <DataTable
        columns={[
          {
            id: 'name',
            header: 'Name',
            render: (row: { name: string }) => row.name,
            sortable: true,
            sortValue: (row) => row.name,
          },
        ]}
        getRowID={(row) => row.name}
        rows={[{ name: 'worker' }, { name: 'api' }]}
      />,
    );

    fireEvent.click(screen.getByRole('button', { name: 'Name' }));
    expect(screen.getAllByRole('cell').map((cell) => cell.textContent)).toEqual(['api', 'worker']);
  });

  it('shows selected rows and bulk actions', async () => {
    const onToggle = vi.fn();

    render(
      <DataTable
        bulkActions={<Button size="sm">Stop</Button>}
        columns={[{ id: 'name', header: 'Name', render: (row: { name: string }) => row.name }]}
        getRowID={(row) => row.name}
        onToggleRow={onToggle}
        rows={[{ name: 'api' }]}
        selectedIDs={new Set(['api'])}
      />,
    );

    expect(screen.getByText('1 selected')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('checkbox', { name: 'Select row api' }));
    expect(onToggle).toHaveBeenCalledWith('api');
  });

  it('closes modal on Escape', async () => {
    const onClose = vi.fn();

    render(
      <Modal onClose={onClose} open title="Confirm">
        Body
      </Modal>,
    );

    fireEvent.keyDown(window, { key: 'Escape' });
    expect(onClose).toHaveBeenCalled();
  });

  it('keeps busy modal open on Escape', async () => {
    const onClose = vi.fn();

    render(
      <Modal busy onClose={onClose} open title="Confirm">
        Body
      </Modal>,
    );

    fireEvent.keyDown(window, { key: 'Escape' });
    expect(onClose).not.toHaveBeenCalled();
  });
});
