import { useEffect } from 'react';

import { Events } from '@wailsio/runtime';

export function useEvent<E extends Events.WailsEventName>(
  name: E,
  callback: Events.WailsEventCallback<E>,
) {
  useEffect(() => Events.On(name, callback), [callback, name]);
}
