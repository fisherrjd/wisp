import { createRouter, createWebHistory } from 'vue-router'
import HomeView from '@/views/HomeView.vue'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', name: 'home', component: HomeView },
    {
      path: '/docs',
      name: 'reference',
      component: () => import('@/views/ReferenceView.vue'),
    },
    {
      path: '/docs/:slug',
      name: 'doc',
      component: () => import('@/views/DocView.vue'),
    },
    {
      path: '/:pathMatch(.*)*',
      name: 'not-found',
      component: () => import('@/views/NotFoundView.vue'),
    },
  ],
  // The sticky header is 3.5rem; a hash landing under it looks like the link
  // missed. scrollBehavior is also what makes cross-page `page.md#section`
  // links from the markdown land on the section.
  scrollBehavior(to, _from, saved) {
    if (saved) return saved
    // DocView lands its own hash: this view is behind an out-in transition, so
    // the heading does not exist yet when scrollBehavior runs.
    if (to.hash) return { el: to.hash, top: 80 }
    return { top: 0 }
  },
})

export default router
