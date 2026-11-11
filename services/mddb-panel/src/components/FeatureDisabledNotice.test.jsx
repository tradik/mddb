// @vitest-environment jsdom
// FeatureDisabledNotice (GO-049): names the switched-off module and leads to
// the Features page, through the given callback or the store.
import { describe, test, expect, vi, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent } from '@testing-library/react';
import { useStore } from '../lib/store';
import FeatureDisabledNotice from './FeatureDisabledNotice';

afterEach(cleanup);

describe('FeatureDisabledNotice', () => {
  test('names the module and calls onOpenFeatures', () => {
    const onOpenFeatures = vi.fn();
    render(<FeatureDisabledNotice feature="geo" onOpenFeatures={onOpenFeatures} />);
    expect(screen.getByRole('heading', { name: 'Geo search is switched off' })).toBeTruthy();
    expect(screen.getByText('geo')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Open Features' }));
    expect(onOpenFeatures).toHaveBeenCalledTimes(1);
  });

  test('without a callback it opens the Features page itself', () => {
    useStore.getState().setViewMode('geo');
    render(<FeatureDisabledNotice feature="geo" />);
    fireEvent.click(screen.getByRole('button', { name: 'Open Features' }));
    expect(useStore.getState().viewMode).toBe('features');
  });
});
