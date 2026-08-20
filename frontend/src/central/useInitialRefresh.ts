import { useEffect } from 'react';

/** Schedules the first remote-state refresh after React commits the view. */
export function useInitialRefresh(refresh: () => void | Promise<void>) {
  useEffect(() => {
    let active = true;
    queueMicrotask(() => {
      if (active) void refresh();
    });
    return () => { active = false; };
  }, [refresh]);
}
