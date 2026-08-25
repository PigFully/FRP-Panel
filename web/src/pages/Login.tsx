import { zodResolver } from '@hookform/resolvers/zod'
import { Visibility, VisibilityOff } from '@mui/icons-material'
import { Box, Button, IconButton, InputAdornment, Stack, TextField, Typography } from '@mui/material'
import { useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useForm } from 'react-hook-form'
import { useNavigate } from 'react-router-dom'
import { z } from 'zod'
import { ApiError } from '../api/client'
import { qk, useLogin } from '../api/hooks'
import { Logo } from '../brand'
import { useToast } from '../components/Toast'

const schema = z.object({
  username: z.string().min(1, '请输入账户'),
  password: z.string().min(1, '请输入密码'),
})
type Form = z.infer<typeof schema>

export default function Login() {
  const { register, handleSubmit, formState: { errors } } = useForm<Form>({ resolver: zodResolver(schema), defaultValues: { username: 'admin', password: '' } })
  const [showPw, setShowPw] = useState(false)
  const login = useLogin()
  const qc = useQueryClient()
  const nav = useNavigate()
  const toast = useToast()

  const onSubmit = async (v: Form) => {
    try {
      await login.mutateAsync(v)
      await qc.invalidateQueries({ queryKey: qk.me })
      nav('/', { replace: true })
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : '登录失败')
    }
  }

  return (
    <Box sx={{ minHeight: '100vh', display: 'grid', placeItems: 'center', p: 2, bgcolor: 'background.default' }}>
      <Stack spacing={3} sx={{ width: '100%', maxWidth: 440 }} component="form" onSubmit={handleSubmit(onSubmit)}>
        <Box sx={{ width: 56, height: 56 }}>
          <Logo size={56} />
        </Box>
        <Box>
          <Typography variant="h4">登录你的账户</Typography>
          <Typography variant="body2" color="text.secondary" sx={{ mt: 1 }}>
            请输入管理员账户与密码以进入 FRPanel 管理面板
          </Typography>
        </Box>
        <TextField label="账户" fullWidth {...register('username')} error={!!errors.username} helperText={errors.username?.message} />
        <TextField
          label="密码"
          type={showPw ? 'text' : 'password'}
          fullWidth
          {...register('password')}
          error={!!errors.password}
          helperText={errors.password?.message}
          InputProps={{
            endAdornment: (
              <InputAdornment position="end">
                <IconButton onClick={() => setShowPw((s) => !s)} edge="end">
                  {showPw ? <VisibilityOff /> : <Visibility />}
                </IconButton>
              </InputAdornment>
            ),
          }}
        />
        <Button type="submit" variant="contained" size="large" fullWidth disabled={login.isPending} sx={{ height: 48 }}>
          {login.isPending ? '登录中…' : '登录'}
        </Button>
      </Stack>
    </Box>
  )
}
