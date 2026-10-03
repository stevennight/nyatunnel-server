import { createContext, useContext } from 'react'
import type { Bootstrap, User } from './types'

export type Session = {
  bootstrap: Bootstrap
  /** The signed-in user as GET /me returned it. */
  user: User
  isAdmin: boolean
  mustSetupTotp: boolean
}

const SessionContext = createContext<Session | null>(null)

export const SessionProvider = SessionContext.Provider

export function useSession(): Session {
  const s = useContext(SessionContext)
  if (!s) throw new Error('useSession outside of the signed-in console')
  return s
}
