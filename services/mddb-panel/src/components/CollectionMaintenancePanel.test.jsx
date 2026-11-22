// @vitest-environment jsdom
// CollectionMaintenancePanel (2.19.11): the page that holds a collection's
// upkeep and its indexing status; both cards have their own suites.
import { describe, test, expect, vi, afterEach } from 'vitest';
import { render, screen, cleanup } from '@testing-library/react';

vi.mock('./CollectionMaintenanceCard', () => ({ default: () => <div>maintenance card</div> }));
vi.mock('./IndexingStatusCard', () => ({ default: () => <div>indexing status card</div> }));

const { default: CollectionMaintenancePanel } = await import('./CollectionMaintenancePanel');

afterEach(cleanup);

describe('CollectionMaintenancePanel', () => {
  test('shows the page title, the maintenance actions and the indexing status', () => {
    render(<CollectionMaintenancePanel />);
    expect(screen.getByRole('heading', { level: 1, name: 'Collection Maintenance' })).toBeTruthy();
    expect(screen.getByText('maintenance card')).toBeTruthy();
    expect(screen.getByText('indexing status card')).toBeTruthy();
  });
});
