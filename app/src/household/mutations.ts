import { useMutation } from '@tanstack/react-query';

import { useHouseholds } from './current-household';
import { createHousehold, joinedMessage, joinHousehold } from './households';

/**
 * `POST /households`. On success the new household becomes current. Never
 * retried automatically: the endpoint is not idempotent, so a retry after a
 * lost response could create a second household. The form offers "Try again".
 */
export function useCreateHouseholdMutation() {
  const { adopt } = useHouseholds();
  return useMutation({
    mutationFn: createHousehold,
    retry: 0,
    // Set on the mutation, not per call, so it still runs if the form closed meanwhile.
    onSuccess: (household) => adopt(household),
  });
}

/** `POST /invites/join`. On success (or `alreadyMember`) the household becomes current, with a message. */
export function useJoinHouseholdMutation() {
  const { adopt, showNotice } = useHouseholds();
  return useMutation({
    mutationFn: joinHousehold,
    retry: 0,
    onSuccess: (result) => {
      showNotice(joinedMessage(result));
      adopt(result.household);
    },
  });
}
