import { BrowserRouter, Navigate, Route, Routes } from 'react-router-dom';
import { AuthGate } from './auth/AuthContext';
import { AuthPage } from './pages/AuthPage';
import { ShelfPage } from './pages/ShelfPage';
import { ReaderPage } from './pages/ReaderPage';
import './App.css';

export default function App() {
  return (
    <AuthGate renderAuth={(mode, onDone) => <AuthPage mode={mode} onDone={onDone} />}>
      <BrowserRouter>
        <Routes>
          <Route path="/" element={<ShelfPage />} />
          <Route path="/read/:id" element={<ReaderPage />} />
          <Route path="*" element={<Navigate to="/" replace />} />
        </Routes>
      </BrowserRouter>
    </AuthGate>
  );
}
